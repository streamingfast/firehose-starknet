package pbstarknet

import (
	"time"

	"github.com/NethermindEth/juno/core/felt"
)

func (x *Block) GetFirehoseBlockID() string {
	f := &felt.Felt{}
	f = f.SetBytes(x.BlockHash)
	return f.String()
}

func (x *Block) GetFirehoseBlockNumber() uint64 {
	return x.BlockNumber
}

func (x *Block) GetFirehoseBlockParentID() string {
	f := &felt.Felt{}
	f = f.SetBytes(x.ParentHash)
	return f.String()
}

func (x *Block) GetFirehoseBlockParentNumber() uint64 {
	return x.BlockNumber - 1
}

func (x *Block) GetFirehoseBlockTime() time.Time {
	return time.Unix(int64(x.Timestamp), 0)
}
