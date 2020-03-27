package core

import (
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"time"

	"github.com/FusionFoundation/go-fusion/common"
	"github.com/FusionFoundation/go-fusion/common/hexutil"
	"github.com/FusionFoundation/go-fusion/consensus/datong"
	"github.com/FusionFoundation/go-fusion/core/types"
	"github.com/FusionFoundation/go-fusion/crypto"
	"github.com/FusionFoundation/go-fusion/rlp"
	"golang.org/x/crypto/sha3"
)

func rlpHash(x interface{}) (h common.Hash) {
	hw := sha3.NewLegacyKeccak256()
	rlp.Encode(hw, x)
	hw.Sum(h[:0])
	return h
}

func GetUniqueHashFromTransaction(tx *types.Transaction) common.Hash {
	return rlpHash(types.NewTransaction(tx.Nonce(), *tx.To(), tx.Value(), tx.Gas(), tx.GasPrice(), tx.Data()))
}

func GetUniqueHashFromMessage(m Message) common.Hash {
	return rlpHash(types.NewTransaction(m.Nonce(), *m.To(), m.Value(), m.Gas(), m.GasPrice(), m.Data()))
}

func (st *StateTransition) handleFsnCall(param *common.FSNCallParam) error {
	height := st.evm.Context.BlockNumber
	timestamp := st.evm.Context.ParentTime.Uint64()

	switch param.Func {
	case common.GenNotationFunc:
		if err := st.state.GenNotation(st.msg.From()); err != nil {
			st.addLog(common.GenNotationFunc, param, common.NewKeyValue("Error", err.Error()))
			return err
		}
		st.addLog(common.GenNotationFunc, param, common.NewKeyValue("notation", st.state.GetNotation(st.msg.From())))
		return nil
	case common.GenAssetFunc:
		genAssetParam := common.GenAssetParam{}
		rlp.DecodeBytes(param.Data, &genAssetParam)
		if err := genAssetParam.Check(height); err != nil {
			st.addLog(common.GenAssetFunc, genAssetParam, common.NewKeyValue("Error", err.Error()))
			return err
		}
		asset := genAssetParam.ToAsset()
		asset.ID = GetUniqueHashFromMessage(st.msg)
		asset.Owner = st.msg.From()
		if err := st.state.GenAsset(asset); err != nil {
			st.addLog(common.GenAssetFunc, genAssetParam, common.NewKeyValue("Error", "unable to gen asset"))
			return err
		}
		st.state.AddBalance(st.msg.From(), asset.ID, asset.Total)
		st.addLog(common.GenAssetFunc, genAssetParam, common.NewKeyValue("AssetID", asset.ID))
		return nil
	case common.SendAssetFunc:
		sendAssetParam := common.SendAssetParam{}
		rlp.DecodeBytes(param.Data, &sendAssetParam)
		if err := sendAssetParam.Check(height); err != nil {
			st.addLog(common.SendAssetFunc, sendAssetParam, common.NewKeyValue("Error", err.Error()))
			return err
		}
		if st.state.GetBalance(sendAssetParam.AssetID, st.msg.From()).Cmp(sendAssetParam.Value) < 0 {
			st.addLog(common.SendAssetFunc, sendAssetParam, common.NewKeyValue("Error", "not enough asset"))
			return fmt.Errorf("not enough asset")
		}
		st.state.SubBalance(st.msg.From(), sendAssetParam.AssetID, sendAssetParam.Value)
		st.state.AddBalance(sendAssetParam.To, sendAssetParam.AssetID, sendAssetParam.Value)
		st.addLog(common.SendAssetFunc, sendAssetParam, common.NewKeyValue("AssetID", sendAssetParam.AssetID))
		return nil
	case common.TimeLockFunc:
		timeLockParam := common.TimeLockParam{}
		rlp.DecodeBytes(param.Data, &timeLockParam)

		// adjust param
		if timeLockParam.Type == common.TimeLockToAsset {
			if timeLockParam.StartTime > uint64(time.Now().Unix()) {
				st.addLog(common.TimeLockFunc, timeLockParam, common.NewKeyValue("LockType", "TimeLockToAsset"), common.NewKeyValue("Error", "Start time must be less than now"))
				return fmt.Errorf("Start time must be less than now")
			}
			timeLockParam.EndTime = common.TimeLockForever
		}
		if err := timeLockParam.Check(height, timestamp); err != nil {
			st.addLog(common.TimeLockFunc, timeLockParam, common.NewKeyValue("Error", err.Error()))
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
			st.addLog(common.TimeLockFunc, timeLockParam, common.NewKeyValue("Error", err.Error()))
			return fmt.Errorf(err.Error())
		}

		switch timeLockParam.Type {
		case common.AssetToTimeLock:
			if st.state.GetBalance(timeLockParam.AssetID, st.msg.From()).Cmp(timeLockParam.Value) < 0 {
				st.addLog(common.TimeLockFunc, timeLockParam, common.NewKeyValue("LockType", "AssetToTimeLock"), common.NewKeyValue("Error", "not enough asset"))
				return fmt.Errorf("not enough asset")
			}
			st.state.SubBalance(st.msg.From(), timeLockParam.AssetID, timeLockParam.Value)

			totalValue := common.NewTimeLock(&common.TimeLockItem{
				StartTime: timestamp,
				EndTime:   common.TimeLockForever,
				Value:     new(big.Int).SetBytes(timeLockParam.Value.Bytes()),
			})
			if st.msg.From() == timeLockParam.To {
				st.state.AddTimeLockBalance(timeLockParam.To, timeLockParam.AssetID, totalValue, height, timestamp)
			} else {
				surplusValue := new(common.TimeLock).Sub(totalValue, needValue)
				if !surplusValue.IsEmpty() {
					st.state.AddTimeLockBalance(st.msg.From(), timeLockParam.AssetID, surplusValue, height, timestamp)
				}
				st.state.AddTimeLockBalance(timeLockParam.To, timeLockParam.AssetID, needValue, height, timestamp)
			}

			st.addLog(common.TimeLockFunc, timeLockParam, common.NewKeyValue("LockType", "AssetToTimeLock"), common.NewKeyValue("AssetID", timeLockParam.AssetID))
			return nil
		case common.TimeLockToTimeLock:
			if st.state.GetTimeLockBalance(timeLockParam.AssetID, st.msg.From()).Cmp(needValue) < 0 {
				st.addLog(common.TimeLockFunc, timeLockParam, common.NewKeyValue("LockType", "TimeLockToTimeLock"), common.NewKeyValue("Error", "not enough time lock balance"))
				return fmt.Errorf("not enough time lock balance")
			}
			st.state.SubTimeLockBalance(st.msg.From(), timeLockParam.AssetID, needValue, height, timestamp)
			st.state.AddTimeLockBalance(timeLockParam.To, timeLockParam.AssetID, needValue, height, timestamp)
			st.addLog(common.TimeLockFunc, timeLockParam, common.NewKeyValue("LockType", "TimeLockToTimeLock"), common.NewKeyValue("AssetID", timeLockParam.AssetID))
			return nil
		case common.TimeLockToAsset:
			if st.state.GetTimeLockBalance(timeLockParam.AssetID, st.msg.From()).Cmp(needValue) < 0 {
				st.addLog(common.TimeLockFunc, timeLockParam, common.NewKeyValue("LockType", "TimeLockToAsset"), common.NewKeyValue("Error", "not enough time lock balance"))
				return fmt.Errorf("not enough time lock balance")
			}
			st.state.SubTimeLockBalance(st.msg.From(), timeLockParam.AssetID, needValue, height, timestamp)
			st.state.AddBalance(timeLockParam.To, timeLockParam.AssetID, timeLockParam.Value)
			st.addLog(common.TimeLockFunc, timeLockParam, common.NewKeyValue("LockType", "TimeLockToAsset"), common.NewKeyValue("AssetID", timeLockParam.AssetID))
			return nil
		case common.SmartTransfer:
			if !common.IsSmartTransferEnabled(height) {
				st.addLog(common.TimeLockFunc, timeLockParam, common.NewKeyValue("LockType", "SmartTransfer"), common.NewKeyValue("Error", "not enabled"))
				return fmt.Errorf("SendTimeLock not enabled")
			}
			timeLockBalance := st.state.GetTimeLockBalance(timeLockParam.AssetID, st.msg.From())
			if timeLockBalance.Cmp(needValue) < 0 {
				timeLockValue := timeLockBalance.GetSpendableValue(start, end)
				assetBalance := st.state.GetBalance(timeLockParam.AssetID, st.msg.From())
				if new(big.Int).Add(timeLockValue, assetBalance).Cmp(timeLockParam.Value) < 0 {
					st.addLog(common.TimeLockFunc, timeLockParam, common.NewKeyValue("LockType", "SmartTransfer"), common.NewKeyValue("Error", "not enough balance"))
					return fmt.Errorf("not enough balance")
				}
				if timeLockValue.Sign() > 0 {
					subTimeLock := common.GetTimeLock(timeLockValue, start, end)
					st.state.SubTimeLockBalance(st.msg.From(), timeLockParam.AssetID, subTimeLock, height, timestamp)
				}
				useAssetAmount := new(big.Int).Sub(timeLockParam.Value, timeLockValue)
				st.state.SubBalance(st.msg.From(), timeLockParam.AssetID, useAssetAmount)
				surplus := common.GetSurplusTimeLock(useAssetAmount, start, end, timestamp)
				if !surplus.IsEmpty() {
					st.state.AddTimeLockBalance(st.msg.From(), timeLockParam.AssetID, surplus, height, timestamp)
				}
			} else {
				st.state.SubTimeLockBalance(st.msg.From(), timeLockParam.AssetID, needValue, height, timestamp)
			}

			if !common.IsWholeAsset(start, end, timestamp) {
				st.state.AddTimeLockBalance(timeLockParam.To, timeLockParam.AssetID, needValue, height, timestamp)
			} else {
				st.state.AddBalance(timeLockParam.To, timeLockParam.AssetID, timeLockParam.Value)
			}
			st.addLog(common.TimeLockFunc, timeLockParam, common.NewKeyValue("LockType", "SmartTransfer"), common.NewKeyValue("AssetID", timeLockParam.AssetID))
			return nil
		}
	case common.BuyTicketFunc:
		from := st.msg.From()
		hash := st.evm.GetHash(height.Uint64() - 1)
		id := crypto.Keccak256Hash(from[:], hash[:])

		if st.state.IsTicketExist(id) {
			st.addLog(common.BuyTicketFunc, param.Data, common.NewKeyValue("Error", "Ticket already exist"))
			return fmt.Errorf(id.String() + " Ticket already exist")
		}

		buyTicketParam := common.BuyTicketParam{}
		rlp.DecodeBytes(param.Data, &buyTicketParam)

		// check buy ticket param
		if common.IsHardFork(2, height) {
			if err := buyTicketParam.Check(height, timestamp); err != nil {
				st.addLog(common.BuyTicketFunc, param.Data, common.NewKeyValue("Error", err.Error()))
				return err
			}
		} else {
			if err := buyTicketParam.Check(height, 0); err != nil {
				st.addLog(common.BuyTicketFunc, param.Data, common.NewKeyValue("Error", err.Error()))
				return err
			}
		}

		start := buyTicketParam.Start
		end := buyTicketParam.End
		value := common.TicketPrice(height)
		var needValue *common.TimeLock

		needValue = common.NewTimeLock(&common.TimeLockItem{
			StartTime: common.MaxUint64(start, timestamp),
			EndTime:   end,
			Value:     value,
		})
		if err := needValue.IsValid(); err != nil {
			st.addLog(common.BuyTicketFunc, param.Data, common.NewKeyValue("Error", err.Error()))
			return fmt.Errorf(err.Error())
		}

		ticket := common.Ticket{
			Owner: from,
			TicketBody: common.TicketBody{
				ID:         id,
				Height:     height.Uint64(),
				StartTime:  start,
				ExpireTime: end,
			},
		}

		useAsset := false
		if st.state.GetTimeLockBalance(common.SystemAssetID, from).Cmp(needValue) < 0 {
			if st.state.GetBalance(common.SystemAssetID, from).Cmp(value) < 0 {
				st.addLog(common.BuyTicketFunc, param.Data, common.NewKeyValue("Error", "not enough time lock or asset balance"))
				return fmt.Errorf("not enough time lock or asset balance")
			}
			useAsset = true
		}

		if useAsset {
			st.state.SubBalance(from, common.SystemAssetID, value)

			totalValue := common.NewTimeLock(&common.TimeLockItem{
				StartTime: timestamp,
				EndTime:   common.TimeLockForever,
				Value:     value,
			})
			surplusValue := new(common.TimeLock).Sub(totalValue, needValue)
			if !surplusValue.IsEmpty() {
				st.state.AddTimeLockBalance(from, common.SystemAssetID, surplusValue, height, timestamp)
			}

		} else {
			st.state.SubTimeLockBalance(from, common.SystemAssetID, needValue, height, timestamp)
		}

		if err := st.state.AddTicket(ticket); err != nil {
			st.addLog(common.BuyTicketFunc, param.Data, common.NewKeyValue("Error", "unable to add ticket"))
			return err
		}
		st.addLog(common.BuyTicketFunc, param.Data, common.NewKeyValue("TicketID", ticket.ID), common.NewKeyValue("TicketOwner", ticket.Owner))
		return nil
	case common.AssetValueChangeFunc:
		assetValueChangeParamEx := common.AssetValueChangeExParam{}
		rlp.DecodeBytes(param.Data, &assetValueChangeParamEx)

		if err := assetValueChangeParamEx.Check(height); err != nil {
			st.addLog(common.AssetValueChangeFunc, assetValueChangeParamEx, common.NewKeyValue("Error", err.Error()))
			return err
		}

		asset, err := st.state.GetAsset(assetValueChangeParamEx.AssetID)
		if err != nil {
			st.addLog(common.AssetValueChangeFunc, assetValueChangeParamEx, common.NewKeyValue("Error", "asset not found"))
			return fmt.Errorf("asset not found")
		}

		if !asset.CanChange {
			st.addLog(common.AssetValueChangeFunc, assetValueChangeParamEx, common.NewKeyValue("Error", "asset can't inc or dec"))
			return fmt.Errorf("asset can't inc or dec")
		}

		if asset.Owner != st.msg.From() {
			st.addLog(common.AssetValueChangeFunc, assetValueChangeParamEx, common.NewKeyValue("Error", "can only be changed by owner"))
			return fmt.Errorf("can only be changed by owner")
		}

		if asset.Owner != assetValueChangeParamEx.To && !assetValueChangeParamEx.IsInc {
			err := fmt.Errorf("decrement can only happen to asset's own account")
			st.addLog(common.AssetValueChangeFunc, assetValueChangeParamEx, common.NewKeyValue("Error", err.Error()))
			return err
		}

		if assetValueChangeParamEx.IsInc {
			st.state.AddBalance(assetValueChangeParamEx.To, assetValueChangeParamEx.AssetID, assetValueChangeParamEx.Value)
			asset.Total = asset.Total.Add(asset.Total, assetValueChangeParamEx.Value)
		} else {
			if st.state.GetBalance(assetValueChangeParamEx.AssetID, assetValueChangeParamEx.To).Cmp(assetValueChangeParamEx.Value) < 0 {
				st.addLog(common.AssetValueChangeFunc, assetValueChangeParamEx, common.NewKeyValue("Error", "not enough asset"))
				return fmt.Errorf("not enough asset")
			}
			st.state.SubBalance(assetValueChangeParamEx.To, assetValueChangeParamEx.AssetID, assetValueChangeParamEx.Value)
			asset.Total = asset.Total.Sub(asset.Total, assetValueChangeParamEx.Value)
		}
		err = st.state.UpdateAsset(asset)
		if err == nil {
			st.addLog(common.AssetValueChangeFunc, assetValueChangeParamEx, common.NewKeyValue("AssetID", assetValueChangeParamEx.AssetID))
		} else {
			st.addLog(common.AssetValueChangeFunc, assetValueChangeParamEx, common.NewKeyValue("Error", "error update asset"))
		}
		return err
	case common.EmptyFunc:
	case common.MakeSwapFunc, common.MakeSwapFuncExt:
		notation := st.state.GetNotation(st.msg.From())
		makeSwapParam := common.MakeSwapParam{}
		rlp.DecodeBytes(param.Data, &makeSwapParam)
		swapId := GetUniqueHashFromMessage(st.msg)

		_, err := st.state.GetSwap(swapId)
		if err == nil {
			st.addLog(common.MakeSwapFunc, makeSwapParam, common.NewKeyValue("Error", "Swap already exist"))
			return fmt.Errorf("Swap already exist")
		}

		if err := makeSwapParam.Check(height, timestamp); err != nil {
			st.addLog(common.MakeSwapFunc, makeSwapParam, common.NewKeyValue("Error", err.Error()))
			return err
		}

		var useAsset bool
		var total *big.Int
		var needValue *common.TimeLock

		if _, err := st.state.GetAsset(makeSwapParam.ToAssetID); err != nil {
			err := fmt.Errorf("ToAssetID's asset not found")
			st.addLog(common.MakeSwapFunc, makeSwapParam, common.NewKeyValue("Error", err.Error()))
			return err
		}

		if makeSwapParam.FromAssetID == common.OwnerUSANAssetID {
			if notation == 0 {
				err := fmt.Errorf("the from address does not have a notation")
				st.addLog(common.MakeSwapFunc, makeSwapParam, common.NewKeyValue("Error", err.Error()))
				return err
			}
			makeSwapParam.MinFromAmount = big.NewInt(1)
			makeSwapParam.SwapSize = big.NewInt(1)
			makeSwapParam.FromStartTime = common.TimeLockNow
			makeSwapParam.FromEndTime = common.TimeLockForever
			useAsset = true
			total = new(big.Int).Mul(makeSwapParam.MinFromAmount, makeSwapParam.SwapSize)
		} else {
			total = new(big.Int).Mul(makeSwapParam.MinFromAmount, makeSwapParam.SwapSize)
			start := makeSwapParam.FromStartTime
			end := makeSwapParam.FromEndTime
			useAsset = start == common.TimeLockNow && end == common.TimeLockForever
			if useAsset == false {
				needValue = common.NewTimeLock(&common.TimeLockItem{
					StartTime: common.MaxUint64(start, timestamp),
					EndTime:   end,
					Value:     total,
				})
				if err := needValue.IsValid(); err != nil {
					st.addLog(common.MakeSwapFunc, makeSwapParam, common.NewKeyValue("Error", err.Error()))
					return fmt.Errorf(err.Error())
				}
			}
		}
		swap := common.Swap{
			ID:            swapId,
			Owner:         st.msg.From(),
			FromAssetID:   makeSwapParam.FromAssetID,
			FromStartTime: makeSwapParam.FromStartTime,
			FromEndTime:   makeSwapParam.FromEndTime,
			MinFromAmount: makeSwapParam.MinFromAmount,
			ToAssetID:     makeSwapParam.ToAssetID,
			ToStartTime:   makeSwapParam.ToStartTime,
			ToEndTime:     makeSwapParam.ToEndTime,
			MinToAmount:   makeSwapParam.MinToAmount,
			SwapSize:      makeSwapParam.SwapSize,
			Targes:        makeSwapParam.Targes,
			Time:          makeSwapParam.Time, // this will mean the block time
			Description:   makeSwapParam.Description,
			Notation:      notation,
		}

		if makeSwapParam.FromAssetID == common.OwnerUSANAssetID {
			if err := st.state.AddSwap(swap); err != nil {
				st.addLog(common.MakeSwapFunc, makeSwapParam, common.NewKeyValue("Error", "System error can't add swap"))
				return err
			}
		} else {
			if useAsset == true {
				if st.state.GetBalance(makeSwapParam.FromAssetID, st.msg.From()).Cmp(total) < 0 {
					st.addLog(common.MakeSwapFunc, makeSwapParam, common.NewKeyValue("Error", "not enough from asset"))
					return fmt.Errorf("not enough from asset")
				}
			} else {
				available := st.state.GetTimeLockBalance(makeSwapParam.FromAssetID, st.msg.From())
				if available.Cmp(needValue) < 0 {
					if param.Func == common.MakeSwapFunc {
						// this was the legacy swap do not do
						// time lock and just return an error
						st.addLog(common.MakeSwapFunc, makeSwapParam, common.NewKeyValue("Error", "not enough time lock or asset balance"))
						return fmt.Errorf("not enough time lock balance")
					}

					if st.state.GetBalance(makeSwapParam.FromAssetID, st.msg.From()).Cmp(total) < 0 {
						st.addLog(common.MakeSwapFunc, makeSwapParam, common.NewKeyValue("Error", "not enough time lock or asset balance"))
						return fmt.Errorf("not enough time lock or asset balance")
					}

					// subtract the asset from the balance
					st.state.SubBalance(st.msg.From(), makeSwapParam.FromAssetID, total)

					totalValue := common.NewTimeLock(&common.TimeLockItem{
						StartTime: timestamp,
						EndTime:   common.TimeLockForever,
						Value:     total,
					})
					st.state.AddTimeLockBalance(st.msg.From(), makeSwapParam.FromAssetID, totalValue, height, timestamp)

				}
			}

			if err := st.state.AddSwap(swap); err != nil {
				st.addLog(common.MakeSwapFunc, makeSwapParam, common.NewKeyValue("Error", "System error can't add swap"))
				return err
			}

			// take from the owner the asset
			if useAsset == true {
				st.state.SubBalance(st.msg.From(), makeSwapParam.FromAssetID, total)
			} else {
				st.state.SubTimeLockBalance(st.msg.From(), makeSwapParam.FromAssetID, needValue, height, timestamp)
			}
		}
		st.addLog(common.MakeSwapFunc, makeSwapParam, common.NewKeyValue("SwapID", swap.ID))
		return nil
	case common.RecallSwapFunc:
		recallSwapParam := common.RecallSwapParam{}
		rlp.DecodeBytes(param.Data, &recallSwapParam)

		swap, err := st.state.GetSwap(recallSwapParam.SwapID)
		if err != nil {
			st.addLog(common.RecallSwapFunc, recallSwapParam, common.NewKeyValue("Error", "Swap not found"))
			return fmt.Errorf("Swap not found")
		}

		if swap.Owner != st.msg.From() {
			st.addLog(common.RecallSwapFunc, recallSwapParam, common.NewKeyValue("Error", "Must be swap onwer can recall"))
			return fmt.Errorf("Must be swap onwer can recall")
		}

		if err := recallSwapParam.Check(height, &swap); err != nil {
			st.addLog(common.RecallSwapFunc, recallSwapParam, common.NewKeyValue("Error", err.Error()))
			return err
		}

		if err := st.state.RemoveSwap(swap.ID); err != nil {
			st.addLog(common.RecallSwapFunc, recallSwapParam, common.NewKeyValue("Error", "Unable to remove swap"))
			return err
		}

		if swap.FromAssetID != common.OwnerUSANAssetID {
			total := new(big.Int).Mul(swap.MinFromAmount, swap.SwapSize)
			start := swap.FromStartTime
			end := swap.FromEndTime
			useAsset := start == common.TimeLockNow && end == common.TimeLockForever

			// return to the owner the balance
			if useAsset == true {
				st.state.AddBalance(st.msg.From(), swap.FromAssetID, total)
			} else {
				needValue := common.NewTimeLock(&common.TimeLockItem{
					StartTime: common.MaxUint64(start, timestamp),
					EndTime:   end,
					Value:     total,
				})
				if err := needValue.IsValid(); err == nil {
					st.state.AddTimeLockBalance(st.msg.From(), swap.FromAssetID, needValue, height, timestamp)
				}
			}
		}
		st.addLog(common.RecallSwapFunc, recallSwapParam, common.NewKeyValue("SwapID", swap.ID))
		return nil
	case common.TakeSwapFunc, common.TakeSwapFuncExt:
		takeSwapParam := common.TakeSwapParam{}
		rlp.DecodeBytes(param.Data, &takeSwapParam)

		swap, err := st.state.GetSwap(takeSwapParam.SwapID)
		if err != nil {
			st.addLog(common.TakeSwapFunc, takeSwapParam, common.NewKeyValue("Error", "swap not found"))
			return fmt.Errorf("Swap not found")
		}

		if err := takeSwapParam.Check(height, &swap, timestamp); err != nil {
			st.addLog(common.TakeSwapFunc, takeSwapParam, common.NewKeyValue("Error", err.Error()))
			return err
		}

		if common.IsPrivateSwapCheckingEnabled(height) {
			if err := common.CheckSwapTargets(swap.Targes, st.msg.From()); err != nil {
				st.addLog(common.TakeSwapFunc, takeSwapParam, common.NewKeyValue("Error", err.Error()))
				return err
			}
		}

		var usanSwap bool
		if swap.FromAssetID == common.OwnerUSANAssetID {
			notation := st.state.GetNotation(swap.Owner)
			if notation == 0 || notation != swap.Notation {
				err := fmt.Errorf("notation in swap is no longer valid")
				st.addLog(common.TakeSwapFunc, takeSwapParam, common.NewKeyValue("Error", err.Error()))
				return err
			}
			usanSwap = true
		} else {
			usanSwap = false
		}

		fromTotal := new(big.Int).Mul(swap.MinFromAmount, takeSwapParam.Size)
		fromStart := swap.FromStartTime
		fromEnd := swap.FromEndTime
		fromUseAsset := fromStart == common.TimeLockNow && fromEnd == common.TimeLockForever

		toTotal := new(big.Int).Mul(swap.MinToAmount, takeSwapParam.Size)
		toStart := swap.ToStartTime
		toEnd := swap.ToEndTime
		toUseAsset := toStart == common.TimeLockNow && toEnd == common.TimeLockForever

		var fromNeedValue *common.TimeLock
		var toNeedValue *common.TimeLock

		if fromUseAsset == false {
			fromNeedValue = common.NewTimeLock(&common.TimeLockItem{
				StartTime: common.MaxUint64(fromStart, timestamp),
				EndTime:   fromEnd,
				Value:     fromTotal,
			})
		}
		if toUseAsset == false {
			toNeedValue = common.NewTimeLock(&common.TimeLockItem{
				StartTime: common.MaxUint64(toStart, timestamp),
				EndTime:   toEnd,
				Value:     toTotal,
			})
		}

		if toUseAsset == true {
			if st.state.GetBalance(swap.ToAssetID, st.msg.From()).Cmp(toTotal) < 0 {
				st.addLog(common.TakeSwapFunc, takeSwapParam, common.NewKeyValue("Error", "not enough from asset"))
				return fmt.Errorf("not enough from asset")
			}
		} else {
			isValid := true
			if err := toNeedValue.IsValid(); err != nil {
				isValid = false
			}
			available := st.state.GetTimeLockBalance(swap.ToAssetID, st.msg.From())
			if isValid && available.Cmp(toNeedValue) < 0 {
				if param.Func == common.TakeSwapFunc {
					// this was the legacy swap do not do
					// time lock and just return an error
					st.addLog(common.TakeSwapFunc, takeSwapParam, common.NewKeyValue("Error", "not enough time lock balance"))
					return fmt.Errorf("not enough time lock balance")
				}

				if st.state.GetBalance(swap.ToAssetID, st.msg.From()).Cmp(toTotal) < 0 {
					st.addLog(common.TakeSwapFunc, takeSwapParam, common.NewKeyValue("Error", "not enough time lock balance"))
					return fmt.Errorf("not enough time lock or asset balance")
				}

				// subtract the asset from the balance
				st.state.SubBalance(st.msg.From(), swap.ToAssetID, toTotal)

				totalValue := common.NewTimeLock(&common.TimeLockItem{
					StartTime: timestamp,
					EndTime:   common.TimeLockForever,
					Value:     toTotal,
				})
				st.state.AddTimeLockBalance(st.msg.From(), swap.ToAssetID, totalValue, height, timestamp)

			}
		}

		swapDeleted := "false"

		if swap.SwapSize.Cmp(takeSwapParam.Size) == 0 {
			if err := st.state.RemoveSwap(swap.ID); err != nil {
				st.addLog(common.TakeSwapFunc, takeSwapParam, common.NewKeyValue("Error", "System Error"))
				return err
			}
			swapDeleted = "true"
		} else {
			swap.SwapSize = swap.SwapSize.Sub(swap.SwapSize, takeSwapParam.Size)
			if err := st.state.UpdateSwap(swap); err != nil {
				st.addLog(common.TakeSwapFunc, takeSwapParam, common.NewKeyValue("Error", "System Error"))
				return err
			}
		}

		if toUseAsset == true {
			st.state.AddBalance(swap.Owner, swap.ToAssetID, toTotal)
			st.state.SubBalance(st.msg.From(), swap.ToAssetID, toTotal)
		} else {
			if err := toNeedValue.IsValid(); err == nil {
				st.state.AddTimeLockBalance(swap.Owner, swap.ToAssetID, toNeedValue, height, timestamp)
				st.state.SubTimeLockBalance(st.msg.From(), swap.ToAssetID, toNeedValue, height, timestamp)
			}
		}

		// credit the taker
		if usanSwap {
			err := st.state.TransferNotation(swap.Notation, swap.Owner, st.msg.From())
			if err != nil {
				st.addLog(common.TakeSwapFunc, takeSwapParam, common.NewKeyValue("Error", "System Error"))
				return err
			}
		} else {
			if fromUseAsset == true {
				st.state.AddBalance(st.msg.From(), swap.FromAssetID, fromTotal)
				// the owner of the swap already had their balance taken away
				// in MakeSwapFunc
				// there is no need to subtract this balance again
				//st.state.SubBalance(swap.Owner, swap.FromAssetID, fromTotal)
			} else {
				if err := fromNeedValue.IsValid(); err == nil {
					st.state.AddTimeLockBalance(st.msg.From(), swap.FromAssetID, fromNeedValue, height, timestamp)
				}
				// the owner of the swap already had their timelock balance taken away
				// in MakeSwapFunc
				// there is no need to subtract this balance again
				// st.state.SubTimeLockBalance(swap.Owner, swap.FromAssetID, fromNeedValue)
			}
		}
		st.addLog(common.TakeSwapFunc, takeSwapParam, common.NewKeyValue("SwapID", swap.ID), common.NewKeyValue("Deleted", swapDeleted))
		return nil
	case common.RecallMultiSwapFunc:
		recallSwapParam := common.RecallMultiSwapParam{}
		rlp.DecodeBytes(param.Data, &recallSwapParam)

		swap, err := st.state.GetMultiSwap(recallSwapParam.SwapID)
		if err != nil {
			st.addLog(common.RecallMultiSwapFunc, recallSwapParam, common.NewKeyValue("Error", "Swap not found"))
			return fmt.Errorf("Swap not found")
		}

		if swap.Owner != st.msg.From() {
			st.addLog(common.RecallMultiSwapFunc, recallSwapParam, common.NewKeyValue("Error", "Must be swap onwer can recall"))
			return fmt.Errorf("Must be swap onwer can recall")
		}

		if err := recallSwapParam.Check(height, &swap); err != nil {
			st.addLog(common.RecallMultiSwapFunc, recallSwapParam, common.NewKeyValue("Error", err.Error()))
			return err
		}

		if err := st.state.RemoveMultiSwap(swap.ID); err != nil {
			st.addLog(common.RecallMultiSwapFunc, recallSwapParam, common.NewKeyValue("Error", "Unable to remove swap"))
			return err
		}

		ln := len(swap.FromAssetID)
		for i := 0; i < ln; i++ {
			total := new(big.Int).Mul(swap.MinFromAmount[i], swap.SwapSize)
			start := swap.FromStartTime[i]
			end := swap.FromEndTime[i]
			useAsset := start == common.TimeLockNow && end == common.TimeLockForever

			// return to the owner the balance
			if useAsset == true {
				st.state.AddBalance(st.msg.From(), swap.FromAssetID[i], total)
			} else {
				needValue := common.NewTimeLock(&common.TimeLockItem{
					StartTime: common.MaxUint64(start, timestamp),
					EndTime:   end,
					Value:     total,
				})

				if err := needValue.IsValid(); err == nil {
					st.state.AddTimeLockBalance(st.msg.From(), swap.FromAssetID[i], needValue, height, timestamp)
				}
			}
		}
		st.addLog(common.RecallMultiSwapFunc, recallSwapParam, common.NewKeyValue("SwapID", swap.ID))
		return nil
	case common.MakeMultiSwapFunc:
		notation := st.state.GetNotation(st.msg.From())
		makeSwapParam := common.MakeMultiSwapParam{}
		rlp.DecodeBytes(param.Data, &makeSwapParam)
		swapID := GetUniqueHashFromMessage(st.msg)

		_, err := st.state.GetSwap(swapID)
		if err == nil {
			st.addLog(common.MakeMultiSwapFunc, makeSwapParam, common.NewKeyValue("Error", "Swap already exist"))
			return fmt.Errorf("Swap already exist")
		}

		if err := makeSwapParam.Check(height, timestamp); err != nil {
			st.addLog(common.MakeMultiSwapFunc, makeSwapParam, common.NewKeyValue("Error", err.Error()))
			return err
		}

		for _, toAssetID := range makeSwapParam.ToAssetID {
			if _, err := st.state.GetAsset(toAssetID); err != nil {
				err := fmt.Errorf("ToAssetID's asset not found")
				st.addLog(common.MakeMultiSwapFunc, makeSwapParam, common.NewKeyValue("Error", err.Error()))
				return err
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
				balance := st.state.GetBalance(makeSwapParam.FromAssetID[i], st.msg.From())
				timelock := st.state.GetTimeLockBalance(makeSwapParam.FromAssetID[i], st.msg.From())
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
					st.addLog(common.MakeMultiSwapFunc, makeSwapParam, common.NewKeyValue("Error", err.Error()))
					return fmt.Errorf(err.Error())
				}
			}

		}
		swap := common.MultiSwap{
			ID:            swapID,
			Owner:         st.msg.From(),
			FromAssetID:   makeSwapParam.FromAssetID,
			FromStartTime: makeSwapParam.FromStartTime,
			FromEndTime:   makeSwapParam.FromEndTime,
			MinFromAmount: makeSwapParam.MinFromAmount,
			ToAssetID:     makeSwapParam.ToAssetID,
			ToStartTime:   makeSwapParam.ToStartTime,
			ToEndTime:     makeSwapParam.ToEndTime,
			MinToAmount:   makeSwapParam.MinToAmount,
			SwapSize:      makeSwapParam.SwapSize,
			Targes:        makeSwapParam.Targes,
			Time:          makeSwapParam.Time, // this will mean the block time
			Description:   makeSwapParam.Description,
			Notation:      notation,
		}

		// check balances first
		for i := 0; i < ln; i++ {
			balance := accountBalances[makeSwapParam.FromAssetID[i]]
			timeLockBalance := accountTimeLockBalances[makeSwapParam.FromAssetID[i]]
			if useAsset[i] == true {
				if balance.Cmp(total[i]) < 0 {
					err = fmt.Errorf("not enough from asset")
					st.addLog(common.MakeMultiSwapFunc, makeSwapParam, common.NewKeyValue("Error", err.Error()))
					return err
				}
				balance.Sub(balance, total[i])
			} else {
				if timeLockBalance.Cmp(needValue[i]) < 0 {
					if balance.Cmp(total[i]) < 0 {
						err = fmt.Errorf("not enough time lock or asset balance")
						st.addLog(common.MakeMultiSwapFunc, makeSwapParam, common.NewKeyValue("Error", err.Error()))
						return err
					}

					balance.Sub(balance, total[i])
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

		// then deduct
		var deductErr error
		for i := 0; i < ln; i++ {
			if useAsset[i] == true {
				if st.state.GetBalance(makeSwapParam.FromAssetID[i], st.msg.From()).Cmp(total[i]) < 0 {
					deductErr = fmt.Errorf("not enough from asset")
					break
				}
			} else {
				available := st.state.GetTimeLockBalance(makeSwapParam.FromAssetID[i], st.msg.From())
				if available.Cmp(needValue[i]) < 0 {

					if st.state.GetBalance(makeSwapParam.FromAssetID[i], st.msg.From()).Cmp(total[i]) < 0 {
						deductErr = fmt.Errorf("not enough time lock or asset balance")
						break
					}

					// subtract the asset from the balance
					st.state.SubBalance(st.msg.From(), makeSwapParam.FromAssetID[i], total[i])

					totalValue := common.NewTimeLock(&common.TimeLockItem{
						StartTime: timestamp,
						EndTime:   common.TimeLockForever,
						Value:     total[i],
					})
					st.state.AddTimeLockBalance(st.msg.From(), makeSwapParam.FromAssetID[i], totalValue, height, timestamp)
				}
			}

			// take from the owner the asset
			if useAsset[i] == true {
				st.state.SubBalance(st.msg.From(), makeSwapParam.FromAssetID[i], total[i])
			} else {
				st.state.SubTimeLockBalance(st.msg.From(), makeSwapParam.FromAssetID[i], needValue[i], height, timestamp)
			}
		}

		if deductErr != nil {
			common.DebugInfo("MakeMultiSwapFunc deduct error, why check balance before have no effect?")
			st.addLog(common.MakeMultiSwapFunc, makeSwapParam, common.NewKeyValue("Error", deductErr.Error()))
			return deductErr
		}

		if err := st.state.AddMultiSwap(swap); err != nil {
			st.addLog(common.MakeMultiSwapFunc, makeSwapParam, common.NewKeyValue("Error", "System error can't add swap"))
			return err
		}

		st.addLog(common.MakeMultiSwapFunc, makeSwapParam, common.NewKeyValue("SwapID", swap.ID))
		return nil
	case common.TakeMultiSwapFunc:
		takeSwapParam := common.TakeMultiSwapParam{}
		rlp.DecodeBytes(param.Data, &takeSwapParam)

		swap, err := st.state.GetMultiSwap(takeSwapParam.SwapID)
		if err != nil {
			st.addLog(common.TakeMultiSwapFunc, takeSwapParam, common.NewKeyValue("Error", "swap not found"))
			return fmt.Errorf("Swap not found")
		}

		if err := takeSwapParam.Check(height, &swap, timestamp); err != nil {
			st.addLog(common.TakeMultiSwapFunc, takeSwapParam, common.NewKeyValue("Error", err.Error()))
			return err
		}

		if common.IsPrivateSwapCheckingEnabled(height) {
			if err := common.CheckSwapTargets(swap.Targes, st.msg.From()); err != nil {
				st.addLog(common.TakeMultiSwapFunc, takeSwapParam, common.NewKeyValue("Error", err.Error()))
				return err
			}
		}

		lnFrom := len(swap.FromAssetID)

		fromUseAsset := make([]bool, lnFrom)
		fromTotal := make([]*big.Int, lnFrom)
		fromStart := make([]uint64, lnFrom)
		fromEnd := make([]uint64, lnFrom)
		fromNeedValue := make([]*common.TimeLock, lnFrom)
		for i := 0; i < lnFrom; i++ {
			fromTotal[i] = new(big.Int).Mul(swap.MinFromAmount[i], takeSwapParam.Size)
			fromStart[i] = swap.FromStartTime[i]
			fromEnd[i] = swap.FromEndTime[i]
			fromUseAsset[i] = fromStart[i] == common.TimeLockNow && fromEnd[i] == common.TimeLockForever

			if fromUseAsset[i] == false {
				fromNeedValue[i] = common.NewTimeLock(&common.TimeLockItem{
					StartTime: common.MaxUint64(fromStart[i], timestamp),
					EndTime:   fromEnd[i],
					Value:     fromTotal[i],
				})
			}
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
				balance := st.state.GetBalance(swap.ToAssetID[i], st.msg.From())
				timelock := st.state.GetTimeLockBalance(swap.ToAssetID[i], st.msg.From())
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
					err = fmt.Errorf("not enough from asset")
					st.addLog(common.TakeMultiSwapFunc, takeSwapParam, common.NewKeyValue("Error", err.Error()))
					return err
				}
				balance.Sub(balance, toTotal[i])
			} else {
				if err := toNeedValue[i].IsValid(); err != nil {
					continue
				}
				if timeLockBalance.Cmp(toNeedValue[i]) < 0 {
					if balance.Cmp(toTotal[i]) < 0 {
						err = fmt.Errorf("not enough time lock or asset balance")
						st.addLog(common.TakeMultiSwapFunc, takeSwapParam, common.NewKeyValue("Error", err.Error()))
						return err
					}

					balance.Sub(balance, toTotal[i])
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

		// then deduct
		var deductErr error
		for i := 0; i < lnTo; i++ {
			if toUseAsset[i] == true {
				if st.state.GetBalance(swap.ToAssetID[i], st.msg.From()).Cmp(toTotal[i]) < 0 {
					deductErr = fmt.Errorf("not enough from asset")
					break
				}
				st.state.SubBalance(st.msg.From(), swap.ToAssetID[i], toTotal[i])
			} else {
				if err := toNeedValue[i].IsValid(); err != nil {
					continue
				}
				available := st.state.GetTimeLockBalance(swap.ToAssetID[i], st.msg.From())
				if available.Cmp(toNeedValue[i]) < 0 {

					if st.state.GetBalance(swap.ToAssetID[i], st.msg.From()).Cmp(toTotal[i]) < 0 {
						deductErr = fmt.Errorf("not enough time lock or asset balance")
						break
					}

					// subtract the asset from the balance
					st.state.SubBalance(st.msg.From(), swap.ToAssetID[i], toTotal[i])

					totalValue := common.NewTimeLock(&common.TimeLockItem{
						StartTime: timestamp,
						EndTime:   common.TimeLockForever,
						Value:     toTotal[i],
					})
					st.state.AddTimeLockBalance(st.msg.From(), swap.ToAssetID[i], totalValue, height, timestamp)
				}
				st.state.SubTimeLockBalance(st.msg.From(), swap.ToAssetID[i], toNeedValue[i], height, timestamp)
			}
		}

		if deductErr != nil {
			common.DebugInfo("TakeMultiSwapFunc deduct error, why check balance before have no effect?")
			st.addLog(common.TakeMultiSwapFunc, takeSwapParam, common.NewKeyValue("Error", deductErr.Error()))
			return deductErr
		}

		swapDeleted := "false"

		if swap.SwapSize.Cmp(takeSwapParam.Size) == 0 {
			if err := st.state.RemoveMultiSwap(swap.ID); err != nil {
				st.addLog(common.TakeMultiSwapFunc, takeSwapParam, common.NewKeyValue("Error", "System Error"))
				return err
			}
			swapDeleted = "true"
		} else {
			swap.SwapSize = swap.SwapSize.Sub(swap.SwapSize, takeSwapParam.Size)
			if err := st.state.UpdateMultiSwap(swap); err != nil {
				st.addLog(common.TakeMultiSwapFunc, takeSwapParam, common.NewKeyValue("Error", "System Error"))
				return err
			}
		}

		// credit the swap owner with to assets
		for i := 0; i < lnTo; i++ {
			if toUseAsset[i] == true {
				st.state.AddBalance(swap.Owner, swap.ToAssetID[i], toTotal[i])
			} else {
				if err := toNeedValue[i].IsValid(); err == nil {
					st.state.AddTimeLockBalance(swap.Owner, swap.ToAssetID[i], toNeedValue[i], height, timestamp)
				}
			}
		}

		// credit the swap take with the from assets
		for i := 0; i < lnFrom; i++ {
			if fromUseAsset[i] == true {
				st.state.AddBalance(st.msg.From(), swap.FromAssetID[i], fromTotal[i])
				// the owner of the swap already had their balance taken away
				// in MakeMultiSwapFunc
				// there is no need to subtract this balance again
				//st.state.SubBalance(swap.Owner, swap.FromAssetID, fromTotal)
			} else {
				if err := fromNeedValue[i].IsValid(); err == nil {
					st.state.AddTimeLockBalance(st.msg.From(), swap.FromAssetID[i], fromNeedValue[i], height, timestamp)
				}
				// the owner of the swap already had their timelock balance taken away
				// in MakeMultiSwapFunc
				// there is no need to subtract this balance again
				// st.state.SubTimeLockBalance(swap.Owner, swap.FromAssetID, fromNeedValue)
			}
		}
		st.addLog(common.TakeMultiSwapFunc, takeSwapParam, common.NewKeyValue("SwapID", swap.ID), common.NewKeyValue("Deleted", swapDeleted))
		return nil
	case common.ReportIllegalFunc:
		if !common.IsMultipleMiningCheckingEnabled(height) {
			return fmt.Errorf("report not enabled")
		}
		report := param.Data
		header1, header2, err := datong.CheckAddingReport(st.state, report, height)
		if err != nil {
			return err
		}
		if err := st.state.AddReport(report); err != nil {
			return err
		}
		delTickets := datong.ProcessReport(header1, header2, st.msg.From(), st.state, height, timestamp)
		enc, _ := rlp.EncodeToBytes(delTickets)
		str := hexutil.Encode(enc)
		st.addLog(common.ReportIllegalFunc, "", common.NewKeyValue("DeleteTickets", str))
		common.DebugInfo("ReportIllegal", "reporter", st.msg.From(), "double-miner", header1.Coinbase, "current-block-height", height, "double-mining-height", header1.Number, "DeleteTickets", delTickets)
		return nil
	}
	return fmt.Errorf("Unsupported")
}

func (st *StateTransition) addLog(typ common.FSNCallFunc, value interface{}, keyValues ...*common.KeyValue) {

	t := reflect.TypeOf(value)
	v := reflect.ValueOf(value)

	maps := make(map[string]interface{})
	if t.Kind() == reflect.Struct {
		for i := 0; i < t.NumField(); i++ {
			if v.Field(i).CanInterface() {
				maps[t.Field(i).Name] = v.Field(i).Interface()
			}
		}
	} else {
		maps["Base"] = value
	}

	for i := 0; i < len(keyValues); i++ {
		maps[keyValues[i].Key] = keyValues[i].Value
	}

	data, _ := json.Marshal(maps)

	topic := common.Hash{}
	topic[common.HashLength-1] = (uint8)(typ)

	st.evm.StateDB.AddLog(&types.Log{
		Address:     common.FSNCallAddress,
		Topics:      []common.Hash{topic},
		Data:        data,
		BlockNumber: st.evm.BlockNumber.Uint64(),
	})
}
