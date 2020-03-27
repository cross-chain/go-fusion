package core

import (
	"bytes"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/FusionFoundation/go-fusion/common"
	"github.com/FusionFoundation/go-fusion/consensus/datong"
	"github.com/FusionFoundation/go-fusion/core/types"
	"github.com/FusionFoundation/go-fusion/rlp"
)

func (pool *TxPool) GetByPredicate(predicate func(*types.Transaction) bool) *types.Transaction {
	return pool.all.GetByPredicate(predicate)
}

func (t *txLookup) GetByPredicate(predicate func(*types.Transaction) bool) *types.Transaction {
	t.lock.RLock()
	defer t.lock.RUnlock()

	for _, tx := range t.all {
		if predicate(tx) {
			return tx
		}
	}
	return nil
}

func (l *txList) FilterInvalid(filter func(*types.Transaction) bool) (types.Transactions, types.Transactions) {
	removed := l.txs.Filter(filter)

	// If the list was strict, filter anything above the lowest nonce
	var invalids types.Transactions

	if l.strict && len(removed) > 0 {
		lowest := uint64(math.MaxUint64)
		for _, tx := range removed {
			if nonce := tx.Nonce(); lowest > nonce {
				lowest = nonce
			}
		}
		invalids = l.txs.Filter(func(tx *types.Transaction) bool { return tx.Nonce() > lowest })
	}
	return removed, invalids
}

func (pool *TxPool) validateAddFsnCallTx(tx *types.Transaction) error {
	if err := pool.validateFsnCallTx(tx); err != nil {
		return err
	}
	if tx.IsBuyTicketTx() {
		from, _ := types.Sender(pool.signer, tx) // already validated
		found := false
		var oldTxHash common.Hash
		pool.all.Range(func(hash common.Hash, tx1 *types.Transaction) bool {
			if hash == tx.Hash() {
				found = true
				return false
			} else if tx1.IsBuyTicketTx() {
				sender, _ := types.Sender(pool.signer, tx1)
				if from == sender {
					// always choose latest buy ticket tx
					oldTxHash = hash
					return false
				}
			}
			return true
		})
		if found == true {
			return fmt.Errorf("%v has already bought a ticket in txpool", from.String())
		}
		if oldTxHash != (common.Hash{}) {
			pool.removeTx(oldTxHash, true)
		}
	}
	return nil
}

func (pool *TxPool) validateReceiveAssetPayableTx(tx *types.Transaction, from common.Address) error {
	header := pool.chain.CurrentBlock().Header()
	height := new(big.Int).Add(header.Number, big.NewInt(1))
	input := tx.Data()
	if !common.IsReceiveAssetPayableTx(height, input) {
		return nil
	}
	if pool.currentState.GetCodeSize(*tx.To()) == 0 {
		return fmt.Errorf("receiveAsset tx receiver must be contract")
	}
	timestamp := uint64(time.Now().Unix())
	p := &common.TransferTimeLockParam{}
	// use `timestamp+600` here to ensure timelock tx with minimum lifetime of 10 minutes,
	// that is endtime of timelock must be greater than or equal to `now + 600 seconds`.
	if err := common.ParseReceiveAssetPayableTxInput(p, input, timestamp+600); err != nil {
		return err
	}
	p.Value = tx.Value()
	p.GasValue = new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas()), tx.GasPrice())
	if !CanTransferTimeLock(pool.currentState, from, p) {
		return ErrInsufficientFunds
	}
	return nil
}

func (pool *TxPool) validateFsnCallTx(tx *types.Transaction) error {
	from, _ := types.Sender(pool.signer, tx) // already validated
	to := tx.To()

	if !common.IsFsnCall(to) {
		if to == nil {
			return nil
		}
		if err := pool.validateReceiveAssetPayableTx(tx, from); err != nil {
			return err
		}
		return nil
	}

	currBlockHeader := pool.chain.CurrentBlock().Header()
	nextBlockNumber := new(big.Int).Add(currBlockHeader.Number, big.NewInt(1))

	state := pool.currentState
	height := common.BigMaxUint64
	timestamp := uint64(time.Now().Unix())

	param := common.FSNCallParam{}
	if err := rlp.DecodeBytes(tx.Data(), &param); err != nil {
		return fmt.Errorf("decode FSNCallParam error")
	}

	fee := common.GetFsnCallFee(to, param.Func)
	fsnValue := big.NewInt(0)

	switch param.Func {
	case common.GenNotationFunc:
		if n := state.GetNotation(from); n != 0 {
			return fmt.Errorf("Account %s has a notation:%d", from.String(), n)
		}

	case common.GenAssetFunc:
		genAssetParam := common.GenAssetParam{}
		rlp.DecodeBytes(param.Data, &genAssetParam)
		if err := genAssetParam.Check(height); err != nil {
			return err
		}
		assetID := GetUniqueHashFromTransaction(tx)
		if _, err := state.GetAsset(assetID); err == nil {
			return fmt.Errorf("%s asset exists", assetID.String())
		}

	case common.SendAssetFunc:
		sendAssetParam := common.SendAssetParam{}
		rlp.DecodeBytes(param.Data, &sendAssetParam)
		if err := sendAssetParam.Check(height); err != nil {
			return err
		}
		if sendAssetParam.AssetID == common.SystemAssetID {
			fsnValue = sendAssetParam.Value
		} else if state.GetBalance(sendAssetParam.AssetID, from).Cmp(sendAssetParam.Value) < 0 {
			return fmt.Errorf("not enough asset")
		}

	case common.TimeLockFunc:
		timeLockParam := common.TimeLockParam{}
		rlp.DecodeBytes(param.Data, &timeLockParam)
		if timeLockParam.Type == common.TimeLockToAsset {
			if timeLockParam.StartTime > timestamp {
				return fmt.Errorf("TimeLockToAsset: Start time must be less than now")
			}
			timeLockParam.EndTime = common.TimeLockForever
		}
		if timeLockParam.To == (common.Address{}) {
			return fmt.Errorf("receiver address must be set and not zero address")
		}
		if err := timeLockParam.Check(height, timestamp); err != nil {
			return err
		}

		start := timeLockParam.StartTime
		end := timeLockParam.EndTime
		if start < timestamp {
			start = timestamp
		}
		needValue := common.NewTimeLock(&common.TimeLockItem{
			StartTime: start,
			EndTime:   end,
			Value:     new(big.Int).SetBytes(timeLockParam.Value.Bytes()),
		})
		if err := needValue.IsValid(); err != nil {
			return err
		}
		switch timeLockParam.Type {
		case common.AssetToTimeLock:
			if timeLockParam.AssetID == common.SystemAssetID {
				fsnValue = timeLockParam.Value
			} else if state.GetBalance(timeLockParam.AssetID, from).Cmp(timeLockParam.Value) < 0 {
				return fmt.Errorf("AssetToTimeLock: not enough asset")
			}
		case common.TimeLockToTimeLock:
			if state.GetTimeLockBalance(timeLockParam.AssetID, from).Cmp(needValue) < 0 {
				return fmt.Errorf("TimeLockToTimeLock: not enough time lock balance")
			}
		case common.TimeLockToAsset:
			if state.GetTimeLockBalance(timeLockParam.AssetID, from).Cmp(needValue) < 0 {
				return fmt.Errorf("TimeLockToAsset: not enough time lock balance")
			}
		case common.SmartTransfer:
			if !common.IsSmartTransferEnabled(nextBlockNumber) {
				return fmt.Errorf("SendTimeLock not enabled")
			}
			timeLockBalance := state.GetTimeLockBalance(timeLockParam.AssetID, from)
			if timeLockBalance.Cmp(needValue) < 0 {
				timeLockValue := timeLockBalance.GetSpendableValue(start, end)
				assetBalance := state.GetBalance(timeLockParam.AssetID, from)
				if new(big.Int).Add(timeLockValue, assetBalance).Cmp(timeLockParam.Value) < 0 {
					return fmt.Errorf("SendTimeLock: not enough balance")
				}
				fsnValue = new(big.Int).Sub(timeLockParam.Value, timeLockValue)
			}
		}

	case common.BuyTicketFunc:
		buyTicketParam := common.BuyTicketParam{}
		rlp.DecodeBytes(param.Data, &buyTicketParam)
		if err := buyTicketParam.Check(height, currBlockHeader.Time); err != nil {
			return err
		}

		start := buyTicketParam.Start
		end := buyTicketParam.End
		value := common.TicketPrice(height)
		needValue := common.NewTimeLock(&common.TimeLockItem{
			StartTime: common.MaxUint64(start, timestamp),
			EndTime:   end,
			Value:     value,
		})
		if err := needValue.IsValid(); err != nil {
			return err
		}

		if state.GetTimeLockBalance(common.SystemAssetID, from).Cmp(needValue) < 0 {
			fsnValue = value
		}

	case common.AssetValueChangeFunc:
		assetValueChangeParamEx := common.AssetValueChangeExParam{}
		rlp.DecodeBytes(param.Data, &assetValueChangeParamEx)

		if err := assetValueChangeParamEx.Check(height); err != nil {
			return err
		}

		asset, err := state.GetAsset(assetValueChangeParamEx.AssetID)
		if err != nil {
			return fmt.Errorf("asset not found")
		}

		if !asset.CanChange {
			return fmt.Errorf("asset can't inc or dec")
		}

		if asset.Owner != from {
			return fmt.Errorf("can only be changed by owner")
		}

		if asset.Owner != assetValueChangeParamEx.To && !assetValueChangeParamEx.IsInc {
			err := fmt.Errorf("decrement can only happen to asset's own account")
			return err
		}

		if !assetValueChangeParamEx.IsInc {
			if state.GetBalance(assetValueChangeParamEx.AssetID, assetValueChangeParamEx.To).Cmp(assetValueChangeParamEx.Value) < 0 {
				return fmt.Errorf("not enough asset")
			}
		}

	case common.EmptyFunc:

	case common.MakeSwapFunc, common.MakeSwapFuncExt:
		makeSwapParam := common.MakeSwapParam{}
		rlp.DecodeBytes(param.Data, &makeSwapParam)
		swapId := GetUniqueHashFromTransaction(tx)

		if _, err := state.GetSwap(swapId); err == nil {
			return fmt.Errorf("MakeSwap: %v Swap already exist", swapId.String())
		}

		if err := makeSwapParam.Check(height, timestamp); err != nil {
			return err
		}

		if _, err := state.GetAsset(makeSwapParam.ToAssetID); err != nil {
			return fmt.Errorf("ToAssetID asset %v not found", makeSwapParam.ToAssetID.String())
		}

		if makeSwapParam.FromAssetID == common.OwnerUSANAssetID {
			notation := state.GetNotation(from)
			if notation == 0 {
				return fmt.Errorf("the from address does not have a notation")
			}
		} else {
			total := new(big.Int).Mul(makeSwapParam.MinFromAmount, makeSwapParam.SwapSize)
			start := makeSwapParam.FromStartTime
			end := makeSwapParam.FromEndTime
			useAsset := start == common.TimeLockNow && end == common.TimeLockForever

			if useAsset == true {
				if makeSwapParam.FromAssetID == common.SystemAssetID {
					fsnValue = total
				} else if state.GetBalance(makeSwapParam.FromAssetID, from).Cmp(total) < 0 {
					return fmt.Errorf("not enough from asset")
				}
			} else {
				needValue := common.NewTimeLock(&common.TimeLockItem{
					StartTime: common.MaxUint64(start, timestamp),
					EndTime:   end,
					Value:     total,
				})
				if err := needValue.IsValid(); err != nil {
					return err
				}
				available := state.GetTimeLockBalance(makeSwapParam.FromAssetID, from)
				if available.Cmp(needValue) < 0 {
					if param.Func == common.MakeSwapFunc {
						// this was the legacy swap do not do
						// time lock and just return an error
						return fmt.Errorf("not enough time lock balance")
					}

					if makeSwapParam.FromAssetID == common.SystemAssetID {
						fsnValue = total
					} else if state.GetBalance(makeSwapParam.FromAssetID, from).Cmp(total) < 0 {
						return fmt.Errorf("not enough time lock or asset balance")
					}
				}
			}
		}

	case common.RecallSwapFunc:
		recallSwapParam := common.RecallSwapParam{}
		rlp.DecodeBytes(param.Data, &recallSwapParam)

		swap, err := state.GetSwap(recallSwapParam.SwapID)
		if err != nil {
			return fmt.Errorf("RecallSwap: %v Swap not found", recallSwapParam.SwapID.String())
		}

		if swap.Owner != from {
			return fmt.Errorf("Must be swap onwer can recall")
		}

		if err := recallSwapParam.Check(height, &swap); err != nil {
			return err
		}

	case common.TakeSwapFunc, common.TakeSwapFuncExt:
		takeSwapParam := common.TakeSwapParam{}
		rlp.DecodeBytes(param.Data, &takeSwapParam)

		swap, err := state.GetSwap(takeSwapParam.SwapID)
		if err != nil {
			return fmt.Errorf("TakeSwap: %v Swap not found", takeSwapParam.SwapID.String())
		}

		if err := takeSwapParam.Check(height, &swap, timestamp); err != nil {
			return err
		}

		if err := common.CheckSwapTargets(swap.Targes, from); err != nil {
			return err
		}

		if swap.FromAssetID == common.OwnerUSANAssetID {
			notation := state.GetNotation(swap.Owner)
			if notation == 0 || notation != swap.Notation {
				return fmt.Errorf("notation in swap is no longer valid")
			}
		}

		toTotal := new(big.Int).Mul(swap.MinToAmount, takeSwapParam.Size)
		toStart := swap.ToStartTime
		toEnd := swap.ToEndTime
		toUseAsset := toStart == common.TimeLockNow && toEnd == common.TimeLockForever

		if toUseAsset == true {
			if swap.ToAssetID == common.SystemAssetID {
				fsnValue = toTotal
			} else if state.GetBalance(swap.ToAssetID, from).Cmp(toTotal) < 0 {
				return fmt.Errorf("not enough from asset")
			}
		} else {
			toNeedValue := common.NewTimeLock(&common.TimeLockItem{
				StartTime: common.MaxUint64(toStart, timestamp),
				EndTime:   toEnd,
				Value:     toTotal,
			})
			isValid := true
			if err := toNeedValue.IsValid(); err != nil {
				isValid = false
			}
			if isValid && state.GetTimeLockBalance(swap.ToAssetID, from).Cmp(toNeedValue) < 0 {
				if param.Func == common.TakeSwapFunc {
					// this was the legacy swap do not do
					// time lock and just return an error
					return fmt.Errorf("not enough time lock balance")
				}

				if swap.ToAssetID == common.SystemAssetID {
					fsnValue = toTotal
				} else if state.GetBalance(swap.ToAssetID, from).Cmp(toTotal) < 0 {
					return fmt.Errorf("not enough time lock or asset balance")
				}
			}
		}

	case common.RecallMultiSwapFunc:
		recallSwapParam := common.RecallMultiSwapParam{}
		rlp.DecodeBytes(param.Data, &recallSwapParam)

		swap, err := state.GetMultiSwap(recallSwapParam.SwapID)
		if err != nil {
			return fmt.Errorf("Swap not found")
		}

		if swap.Owner != from {
			return fmt.Errorf("Must be swap onwer can recall")
		}

		if err := recallSwapParam.Check(height, &swap); err != nil {
			return err
		}

	case common.MakeMultiSwapFunc:
		makeSwapParam := common.MakeMultiSwapParam{}
		rlp.DecodeBytes(param.Data, &makeSwapParam)
		swapID := GetUniqueHashFromTransaction(tx)

		_, err := state.GetSwap(swapID)
		if err == nil {
			return fmt.Errorf("Swap already exist")
		}

		if err := makeSwapParam.Check(height, timestamp); err != nil {
			return err
		}

		for _, toAssetID := range makeSwapParam.ToAssetID {
			if _, err := state.GetAsset(toAssetID); err != nil {
				return fmt.Errorf("ToAssetID asset %v not found", toAssetID.String())
			}
		}

		ln := len(makeSwapParam.FromAssetID)

		useAsset := make([]bool, ln)
		total := make([]*big.Int, ln)
		needValue := make([]*common.TimeLock, ln)

		accountBalances := make(map[common.Hash]*big.Int)
		accountTimeLockBalances := make(map[common.Hash]*common.TimeLock)

		for i := 0; i < ln; i++ {
			if _, exist := accountBalances[makeSwapParam.FromAssetID[i]]; !exist {
				balance := state.GetBalance(makeSwapParam.FromAssetID[i], from)
				timelock := state.GetTimeLockBalance(makeSwapParam.FromAssetID[i], from)
				accountBalances[makeSwapParam.FromAssetID[i]] = new(big.Int).Set(balance)
				accountTimeLockBalances[makeSwapParam.FromAssetID[i]] = timelock.Clone()
			}

			total[i] = new(big.Int).Mul(makeSwapParam.MinFromAmount[i], makeSwapParam.SwapSize)
			start := makeSwapParam.FromStartTime[i]
			end := makeSwapParam.FromEndTime[i]
			useAsset[i] = start == common.TimeLockNow && end == common.TimeLockForever
			if useAsset[i] == false {
				needValue[i] = common.NewTimeLock(&common.TimeLockItem{
					StartTime: common.MaxUint64(start, timestamp),
					EndTime:   end,
					Value:     total[i],
				})
				if err := needValue[i].IsValid(); err != nil {
					return err
				}
			}
		}

		ln = len(makeSwapParam.FromAssetID)
		// check balances first
		for i := 0; i < ln; i++ {
			balance := accountBalances[makeSwapParam.FromAssetID[i]]
			timeLockBalance := accountTimeLockBalances[makeSwapParam.FromAssetID[i]]
			if useAsset[i] == true {
				if balance.Cmp(total[i]) < 0 {
					return fmt.Errorf("not enough from asset")
				}
				balance.Sub(balance, total[i])
				if makeSwapParam.FromAssetID[i] == common.SystemAssetID {
					fsnValue.Add(fsnValue, total[i])
				}
			} else {
				if timeLockBalance.Cmp(needValue[i]) < 0 {
					if balance.Cmp(total[i]) < 0 {
						return fmt.Errorf("not enough time lock or asset balance")
					}

					balance.Sub(balance, total[i])
					if makeSwapParam.FromAssetID[i] == common.SystemAssetID {
						fsnValue.Add(fsnValue, total[i])
					}
					totalValue := common.NewTimeLock(&common.TimeLockItem{
						StartTime: timestamp,
						EndTime:   common.TimeLockForever,
						Value:     total[i],
					})
					timeLockBalance.Add(timeLockBalance, totalValue)
				}
				timeLockBalance.Sub(timeLockBalance, needValue[i])
			}
		}

	case common.TakeMultiSwapFunc:
		takeSwapParam := common.TakeMultiSwapParam{}
		rlp.DecodeBytes(param.Data, &takeSwapParam)

		swap, err := state.GetMultiSwap(takeSwapParam.SwapID)
		if err != nil {
			return fmt.Errorf("Swap not found")
		}

		if err := takeSwapParam.Check(height, &swap, timestamp); err != nil {
			return err
		}

		if err := common.CheckSwapTargets(swap.Targes, from); err != nil {
			return err
		}

		lnTo := len(swap.ToAssetID)

		toUseAsset := make([]bool, lnTo)
		toTotal := make([]*big.Int, lnTo)
		toStart := make([]uint64, lnTo)
		toEnd := make([]uint64, lnTo)
		toNeedValue := make([]*common.TimeLock, lnTo)

		accountBalances := make(map[common.Hash]*big.Int)
		accountTimeLockBalances := make(map[common.Hash]*common.TimeLock)

		for i := 0; i < lnTo; i++ {
			if _, exist := accountBalances[swap.ToAssetID[i]]; !exist {
				balance := state.GetBalance(swap.ToAssetID[i], from)
				timelock := state.GetTimeLockBalance(swap.ToAssetID[i], from)
				accountBalances[swap.ToAssetID[i]] = new(big.Int).Set(balance)
				accountTimeLockBalances[swap.ToAssetID[i]] = timelock.Clone()
			}

			toTotal[i] = new(big.Int).Mul(swap.MinToAmount[i], takeSwapParam.Size)
			toStart[i] = swap.ToStartTime[i]
			toEnd[i] = swap.ToEndTime[i]
			toUseAsset[i] = toStart[i] == common.TimeLockNow && toEnd[i] == common.TimeLockForever
			if toUseAsset[i] == false {
				toNeedValue[i] = common.NewTimeLock(&common.TimeLockItem{
					StartTime: common.MaxUint64(toStart[i], timestamp),
					EndTime:   toEnd[i],
					Value:     toTotal[i],
				})
			}
		}

		// check to account balances
		for i := 0; i < lnTo; i++ {
			balance := accountBalances[swap.ToAssetID[i]]
			timeLockBalance := accountTimeLockBalances[swap.ToAssetID[i]]
			if toUseAsset[i] == true {
				if balance.Cmp(toTotal[i]) < 0 {
					return fmt.Errorf("not enough from asset")
				}
				balance.Sub(balance, toTotal[i])
				if swap.ToAssetID[i] == common.SystemAssetID {
					fsnValue.Add(fsnValue, toTotal[i])
				}
			} else {
				if err := toNeedValue[i].IsValid(); err != nil {
					continue
				}
				if timeLockBalance.Cmp(toNeedValue[i]) < 0 {
					if balance.Cmp(toTotal[i]) < 0 {
						return fmt.Errorf("not enough time lock or asset balance")
					}

					balance.Sub(balance, toTotal[i])
					if swap.ToAssetID[i] == common.SystemAssetID {
						fsnValue.Add(fsnValue, toTotal[i])
					}
					totalValue := common.NewTimeLock(&common.TimeLockItem{
						StartTime: timestamp,
						EndTime:   common.TimeLockForever,
						Value:     toTotal[i],
					})
					timeLockBalance.Add(timeLockBalance, totalValue)
				}
				timeLockBalance.Sub(timeLockBalance, toNeedValue[i])
			}
		}

	case common.ReportIllegalFunc:
		if _, _, err := datong.CheckAddingReport(state, param.Data, nil); err != nil {
			return err
		}
		oldtx := pool.GetByPredicate(func(trx *types.Transaction) bool {
			if trx == tx {
				return false
			}
			p := common.FSNCallParam{}
			rlp.DecodeBytes(trx.Data(), &p)
			return param.Func == common.ReportIllegalFunc && bytes.Equal(p.Data, param.Data)
		})
		if oldtx != nil {
			return fmt.Errorf("already reported in pool")
		}

	default:
		return fmt.Errorf("Unsupported FsnCall func '%v'", param.Func.Name())
	}
	// check gas, fee and value
	mgval := new(big.Int).Mul(new(big.Int).SetUint64(tx.Gas()), tx.GasPrice())
	mgval.Add(mgval, fee)
	mgval.Add(mgval, fsnValue)
	if balance := state.GetBalance(common.SystemAssetID, from); balance.Cmp(mgval) < 0 {
		return fmt.Errorf("insufficient balance(%v), need %v = (gas:%v * price:%v + value:%v + fee:%v)", balance, mgval, tx.Gas(), tx.GasPrice(), fsnValue, fee)
	}
	return nil
}
