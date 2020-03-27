package datong

import (
	"github.com/FusionFoundation/go-fusion/common"
	"github.com/FusionFoundation/go-fusion/consensus"
	"github.com/FusionFoundation/go-fusion/core/types"
	"github.com/FusionFoundation/go-fusion/rpc"
)

// API wacom
type API struct {
	chain consensus.ChainReader
}

func getSnapshotByHeader(header *types.Header) (*Snapshot, error) {
	// Ensure we have an actually valid block and return its snapshot
	if header == nil {
		return nil, errUnknownBlock
	}
	snap, err := NewSnapshotFromHeader(header)
	if err != nil {
		if header.Number.Uint64() == 0 {
			// return an empty snapshot
			return newSnapshot().ToShow(), nil
		}
		return nil, err
	}
	return snap, nil
}

// GetSnapshot wacom
func (api *API) GetSnapshot(number *rpc.BlockNumber) (*Snapshot, error) {
	var header *types.Header
	if number == nil || *number == rpc.LatestBlockNumber {
		header = api.chain.CurrentHeader()
	} else {
		header = api.chain.GetHeaderByNumber(uint64(number.Int64()))
	}
	return getSnapshotByHeader(header)
}

// GetSnapshotAtHash wacom
func (api *API) GetSnapshotAtHash(hash common.Hash) (*Snapshot, error) {
	header := api.chain.GetHeaderByHash(hash)
	return getSnapshotByHeader(header)
}
