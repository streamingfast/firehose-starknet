package pbstarknet

import "time"

func (x *Block) GetFirehoseBlockID() string {
	return x.BlockHash
}

func (x *Block) GetFirehoseBlockNumber() uint64 {
	return x.BlockNumber
}

func (x *Block) GetFirehoseBlockParentID() string {
	return x.ParentHash
}

func (x *Block) GetFirehoseBlockParentNumber() uint64 {
	return x.BlockNumber - 1
}

func (x *Block) GetFirehoseBlockTime() time.Time {
	return time.Unix(int64(x.Timestamp), 0)
}
