package vm

import (
	"errors"
	"math/big"

	"github.com/FusionFoundation/go-fusion/common"
	"github.com/FusionFoundation/go-fusion/params"
)

var (
	FSNContractAddress = common.HexToAddress("0x9999999999999999999999999999999999999999")

	ErrUnknownFunc            = errors.New("unknown func type")
	ErrNotEnoughBalance       = errors.New("not enough balance")
	ErrWrongTimeRange         = errors.New("wrong time range")
	ErrValueOverflow          = errors.New("value overflow")
	ErrWrongLenOfInput        = errors.New("wrong length of input")
	ErrFcInvalidSendAssetFlag = errors.New("invalid send asset flag")

	ErrMustCallByContract      = errors.New("must call by contract")
	ErrForbidCallByContract    = errors.New("forbid call by contract")
	ErrForbidDelegateCall      = errors.New("forbid delegate call")
	ErrDataError               = errors.New("data error")
	ErrToAddressMustBeContract = errors.New("receiver address must be contract")
)

type (
	CanTransferTimeLockFunc func(db StateDB, addr common.Address, p *common.TransferTimeLockParam) bool
	TransferTimeLockFunc    func(db StateDB, sender, recipient common.Address, p *common.TransferTimeLockParam)
)

type FcFuncType uint8

const (
	FcUnknownFunc FcFuncType = iota
	FcSendAsset              // 1
)

func (f FcFuncType) Name() string {
	switch f {
	case FcSendAsset:
		return "sendAsset"
	}
	return "unknown"
}

type FSNContract struct {
	evm      *EVM
	contract *Contract
	input    []byte
}

func NewFSNContract(evm *EVM, contract *Contract) *FSNContract {
	return &FSNContract{
		evm:      evm,
		contract: contract,
	}
}

func (c *FSNContract) RequiredGas(input []byte) uint64 {
	return params.FsnContractGas
}

func (c *FSNContract) Run(input []byte) (ret []byte, err error) {
	c.input = input
	err = ErrUnknownFunc
	funcType := FcUnknownFunc
	if len(c.input) >= 32 {
		funcType = FcFuncType(c.getBigInt(0).Uint64())
		switch funcType {
		case FcSendAsset:
			ret, err = c.sendAsset()
		}
	}
	if err != nil {
		common.DebugInfo("Run FSNContract error",
			"func", funcType.Name(),
			"input", input,
			"err", err,
		)
		return toErrData(err), err
	}
	return ret, err
}

func (c *FSNContract) sendAsset() ([]byte, error) {
	_, err := c.contract.GetParentCaller()
	if err != nil {
		return nil, err
	}
	p, err := c.parseParams()
	if err != nil {
		return nil, err
	}
	from := c.contract.Caller()
	to := p.address

	tranferTimeLockParam := &common.TransferTimeLockParam{
		AssetID:     p.asset,
		StartTime:   p.start,
		EndTime:     p.end,
		Timestamp:   c.evm.Time.Uint64(),
		Flag:        p.flag,
		Value:       p.value,
		GasValue:    nil,
		BlockNumber: c.evm.BlockNumber,
		IsReceive:   false,
	}

	state := c.evm.StateDB
	if !c.evm.CanTransferTimeLock(state, from, tranferTimeLockParam) {
		return nil, ErrNotEnoughBalance
	}
	c.evm.TransferTimeLock(state, from, to, tranferTimeLockParam)

	return toOKData("sendAsset"), nil
}

func (c *FSNContract) getBigInt(pos uint64) *big.Int {
	return new(big.Int).SetBytes(getData(c.input, pos, 32))
}

func (c *FSNContract) getUint64(pos uint64) (uint64, bool) {
	return common.GetUint64(c.input, pos, 32)
}

type FcParams struct {
	asset   common.Hash
	address common.Address
	value   *big.Int
	start   uint64
	end     uint64
	flag    common.FcSendAssetFlag
}

func (c *FSNContract) parseParams() (*FcParams, error) {
	p := &FcParams{}
	var overflow bool

	pos := uint64(32)
	p.asset = common.BytesToHash(getData(c.input, pos, 32))
	pos += 32
	p.address = common.BytesToAddress(getData(c.input, pos, 32))
	pos += 32
	p.value = c.getBigInt(pos)
	pos += 32
	if p.start, overflow = c.getUint64(pos); overflow {
		return nil, ErrValueOverflow
	}
	pos += 32
	if p.end, overflow = c.getUint64(pos); overflow {
		return nil, ErrValueOverflow
	}
	pos += 32
	biFlag := c.getBigInt(pos)
	pos += 32
	if biFlag.Cmp(big.NewInt(int64(common.FcInvalidSendAssetFlag))) >= 0 {
		return nil, ErrFcInvalidSendAssetFlag
	}
	p.flag = common.FcSendAssetFlag(biFlag.Uint64())

	if uint64(len(c.input)) != pos {
		return nil, ErrWrongLenOfInput
	}

	// adjust
	timestamp := c.evm.Context.Time.Uint64()
	if p.start < timestamp {
		p.start = timestamp
	}
	if p.end == 0 {
		p.end = common.TimeLockForever
	}

	// check
	if p.start > p.end {
		return nil, ErrWrongTimeRange
	}
	return p, nil
}

func toOKData(str string) []byte {
	return []byte("Ok: " + str)
}

func toErrData(err error) []byte {
	return []byte("Error: " + err.Error())
}

func (c *Contract) GetParentCaller() (common.Address, error) {
	parent, ok := c.caller.(*Contract)
	if !ok {
		return common.Address{}, ErrMustCallByContract
	}
	return parent.CallerAddress, nil
}

func getPrecompiledContracts(evm *EVM, codeAddr *common.Address, contract *Contract) PrecompiledContract {
	if codeAddr == nil {
		return nil
	}
	if common.IsHardFork(2, evm.BlockNumber) {
		if *codeAddr == FSNContractAddress {
			return NewFSNContract(evm, contract)
		}
	}
	precompiles := PrecompiledContractsHomestead
	if evm.chainRules.IsByzantium {
		precompiles = PrecompiledContractsByzantium
	}
	if evm.chainRules.IsIstanbul {
		precompiles = PrecompiledContractsIstanbul
	}
	return precompiles[*codeAddr]
}
