package datong

import (
	"math/big"

	"github.com/FusionFoundation/go-fusion/common"
	"github.com/FusionFoundation/go-fusion/core/state"
)

//-------------------------- vote1 fork -------------------------
func ApplyVote1HardFork(statedb *state.StateDB, blockNumber *big.Int, timestamp uint64) {
	for _, addr := range common.Vote1DrainList {
		statedb.TransferAll(addr, common.Vote1RefundAddress, blockNumber, timestamp)
	}
}
