package rpc

import (
	"context"
	"fmt"
	"strings"
	"time"

	pbstarknet "firehose-starknet/pb/sf/starknet/type/v1"

	"github.com/NethermindEth/juno/core/felt"
	starknetRPC "github.com/NethermindEth/starknet.go/rpc"
	"github.com/ethereum/go-ethereum/common/hexutil"
	pbbstream "github.com/streamingfast/bstream/pb/sf/bstream/v1"
	"github.com/streamingfast/eth-go"
	ethRPC "github.com/streamingfast/eth-go/rpc"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Fetcher struct {
	starknetClients          *firecoreRPC.Clients[*starknetRPC.Provider]
	ethClients               *firecoreRPC.Clients[*ethRPC.Client]
	fetchInterval            time.Duration
	latestBlockRetryInterval time.Duration
	logger                   *zap.Logger
	latestBlockNum           uint64

	ethCallLIBParams ethRPC.CallParams
}

func NewFetcher(
	starknetClients *firecoreRPC.Clients[*starknetRPC.Provider],
	ethClient *firecoreRPC.Clients[*ethRPC.Client],
	fetchLIBContractAddress string,
	fetchInterval time.Duration,
	latestBlockRetryInterval time.Duration,
	logger *zap.Logger) *Fetcher {
	return &Fetcher{
		starknetClients:          starknetClients,
		ethClients:               ethClient,
		fetchInterval:            fetchInterval,
		latestBlockRetryInterval: latestBlockRetryInterval,
		logger:                   logger,
		ethCallLIBParams:         newEthCallLIBParams(fetchLIBContractAddress),
	}
}

func (f *Fetcher) IsBlockAvailable(blockNum uint64) bool {
	return blockNum <= f.latestBlockNum
}

func (f *Fetcher) Fetch(ctx context.Context, requestBlockNum uint64) (b *pbbstream.Block, skipped bool, err error) {
	f.logger.Info("fetching block", zap.Uint64("block_num", requestBlockNum))

	sleepDuration := time.Duration(0)
	for f.latestBlockNum < requestBlockNum {
		time.Sleep(sleepDuration)

		f.latestBlockNum, err = f.fetchLatestBlockNum(ctx)
		if err != nil {
			return nil, false, fmt.Errorf("fetching latest block num: %w", err)
		}

		f.logger.Info("got latest block num", zap.Uint64("latest_block_num", f.latestBlockNum), zap.Uint64("requested_block_num", requestBlockNum))

		if f.latestBlockNum >= requestBlockNum {
			break
		}
		sleepDuration = f.latestBlockRetryInterval
	}

	f.logger.Info("fetching block", zap.Uint64("block_num", requestBlockNum))
	//at this time we getting the most recent accepted block number (L2)
	blockWithReceipts, err := f.fetchBlock(ctx, requestBlockNum)
	if err != nil {
		return nil, false, fmt.Errorf("fetching block %d: %w", requestBlockNum, err)
	}
	f.logger.Debug("block fetched successfully", zap.Uint64("block_num", requestBlockNum))

	lib, err := f.fetchLIB(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("fetching LIB: %w", err)
	}

	if lib >= requestBlockNum {
		lib = requestBlockNum - 1
	}

	//todo: track state update
	// u, err := f.rpcClients[0].StateUpdate(ctx, starknetRPC.BlockID(requestBlockNum))
	// if err != nil {
	//	return nil, false, fmt.Errorf("fetching state update for block %d: %w", requestBlockNum, err)
	// }
	// u.StateDiff.StorageDiffs[0].StorageEntries[0].Value.

	f.logger.Info("converting block", zap.Uint64("block_num", requestBlockNum))
	bstreamBlock, err := convertBlock(blockWithReceipts)
	if err != nil {
		return nil, false, fmt.Errorf("converting block %d from rpc response: %w", requestBlockNum, err)
	}

	bstreamBlock.LibNum = lib

	return bstreamBlock, false, nil

}

func (f *Fetcher) fetchBlockNumber(ctx context.Context, blockHash *felt.Felt) (uint64, error) {
	return firecoreRPC.WithClients(f.starknetClients, func(client *starknetRPC.Provider) (uint64, error) {
		bn, err := client.BlockWithTxHashes(ctx, starknetRPC.WithBlockHash(blockHash))
		if err != nil {
			return 0, fmt.Errorf("unable to fetch block number: %w", err)
		}
		return bn.(*starknetRPC.BlockTxHashes).BlockNumber, nil
	})
}

func (f *Fetcher) fetchLatestBlockNum(ctx context.Context) (uint64, error) {
	return firecoreRPC.WithClients(f.starknetClients, func(client *starknetRPC.Provider) (uint64, error) {
		blockNum, err := client.BlockNumber(ctx)
		if err != nil {
			return 0, fmt.Errorf("unable to fetch latest block num: %w", err)
		}
		return blockNum, nil
	})
}

func (f *Fetcher) fetchLIB(ctx context.Context) (uint64, error) {
	return firecoreRPC.WithClients(f.ethClients, func(client *ethRPC.Client) (uint64, error) {
		blockNum, err := client.Call(ctx, f.ethCallLIBParams)
		if err != nil {
			return 0, fmt.Errorf("unable to fetch LIB: %w", err)
		}

		blockNum = cleanBlockNum(blockNum)
		b, err := hexutil.DecodeUint64(blockNum)
		if err != nil {
			return 0, fmt.Errorf("unable to parse block number %s: %w", blockNum, err)
		}

		return b, nil
	})
}

func (f *Fetcher) fetchBlock(ctx context.Context, requestBlockNum uint64) (*starknetRPC.BlockWithReceipts, error) {
	return firecoreRPC.WithClients(f.starknetClients, func(client *starknetRPC.Provider) (*starknetRPC.BlockWithReceipts, error) {
		b, err := client.BlockWithReceipts(ctx, starknetRPC.WithBlockNumber(requestBlockNum))
		if err != nil {
			return nil, fmt.Errorf("unable to fetch block: %w", err)
		}
		return b.(*starknetRPC.BlockWithReceipts), nil
	})
}

func cleanBlockNum(blockNum string) string {
	blockNum = strings.TrimPrefix(blockNum, "0x")
	blockNum = strings.TrimLeft(blockNum, "0")
	blockNum = fmt.Sprintf("0x%s", blockNum)
	return blockNum
}

func newEthCallLIBParams(contractAddress string) ethRPC.CallParams {
	return ethRPC.CallParams{
		Data: eth.MustNewMethodDef("stateBlockNumber() (int256)").NewCall().MustEncode(),
		To:   eth.MustNewAddress(contractAddress),
	}
}

func convertBlock(b *starknetRPC.BlockWithReceipts) (*pbbstream.Block, error) {
	block := &pbstarknet.Block{
		BlockHash:        b.BlockHash.String(),
		BlockNumber:      b.BlockNumber,
		L1DaMode:         convertL1DAMode(b.L1DAMode),
		NewRoot:          b.NewRoot.String(),
		ParentHash:       b.ParentHash.String(),
		SequencerAddress: b.SequencerAddress.String(),
		StarknetVersion:  b.StarknetVersion,
		Timestamp:        b.Timestamp,
		L1DataGasPrice:   convertL1DataGasPrice(b.L1DataGasPrice),
		L1GasPrice:       convertL1GasPrice(b.L1GasPrice),
	}

	transactions := make([]*pbstarknet.TransactionWithReceipt, 0, len(b.Transactions))
	for _, tx := range b.Transactions {
		t, err := convertTransactionWithReceipt(tx)
		if err != nil {
			return nil, fmt.Errorf("converting transaction: %w", err)
		}
		transactions = append(transactions, t)
	}

	block.Transaction = transactions

	anyBlock, err := anypb.New(block)
	if err != nil {
		return nil, fmt.Errorf("unable to create anypb: %w", err)
	}

	var parentBlockNum uint64
	if block.BlockNumber > 0 {
		parentBlockNum = block.BlockNumber - 1
	}

	libNum := parentBlockNum

	bstreamBlock := &pbbstream.Block{
		Number:    block.BlockNumber,
		Id:        block.BlockHash,
		ParentId:  block.ParentHash,
		Timestamp: timestamppb.New(time.Unix(int64(block.Timestamp), 0)),
		LibNum:    libNum,
		ParentNum: parentBlockNum,
		Payload:   anyBlock,
	}

	return bstreamBlock, nil
}

func convertL1DAMode(mode starknetRPC.L1DAMode) pbstarknet.L1_DA_MODE {
	switch mode {
	case starknetRPC.L1DAModeBlob:
		return pbstarknet.L1_DA_MODE_BLOB
	case starknetRPC.L1DAModeCalldata:
		return pbstarknet.L1_DA_MODE_CALLDATA
	default:
		panic(fmt.Errorf("unknown L1DAMode %v", mode))
	}
}

func convertL1DataGasPrice(l starknetRPC.ResourcePrice) *pbstarknet.L1GasPrice {
	f := "0x0"
	if l.PriceInFRI != nil {
		f = l.PriceInFRI.String()
	}
	w := "0x0"
	if l.PriceInWei != nil {
		w = l.PriceInWei.String()
	}
	return &pbstarknet.L1GasPrice{
		PriceInFri: f,
		PriceInWei: w,
	}
}
func convertL1GasPrice(l starknetRPC.ResourcePrice) *pbstarknet.L1GasPrice {
	f := "0x0"
	if l.PriceInFRI != nil {
		f = l.PriceInFRI.String()
	}
	w := "0x0"
	if l.PriceInWei != nil {
		w = l.PriceInWei.String()
	}
	return &pbstarknet.L1GasPrice{
		PriceInFri: f,
		PriceInWei: w,
	}
}

func convertTransactionWithReceipt(tx starknetRPC.TransactionWithReceipt) (*pbstarknet.TransactionWithReceipt, error) {
	t := &pbstarknet.TransactionWithReceipt{}
	convertAndSetTransaction(t, tx.Transaction.Transaction)

	t.Receipt = convertAndSetReceipt(tx.Receipt.TransactionReceipt)

	return t, nil
}

func convertAndSetTransaction(out *pbstarknet.TransactionWithReceipt, in starknetRPC.Transaction) {
	switch i := in.(type) {
	case starknetRPC.InvokeTxnV0:
		out.Transaction = convertInvokeTransactionV0(i)
	case starknetRPC.InvokeTxnV1:
		out.Transaction = convertInvokeTransactionV1(i)
	case starknetRPC.InvokeTxnV3:
		out.Transaction = convertInvokeTransactionV3(i)
	case starknetRPC.L1HandlerTxn:
		out.Transaction = convertL1HandlerTransaction(i)
	case starknetRPC.DeclareTxnV0:
		out.Transaction = convertDeclareTransactionV0(i)
	case starknetRPC.DeclareTxnV1:
		out.Transaction = convertDeclareTransactionV1(i)
	case starknetRPC.DeclareTxnV2:
		out.Transaction = convertDeclareTransactionV2(i)
	case starknetRPC.DeclareTxnV3:
		out.Transaction = convertDeclareTransactionV3(i)
	case starknetRPC.DeployTxn:
		out.Transaction = convertDeployTransactionV0(i)
	case starknetRPC.DeployAccountTxn:
		out.Transaction = convertDeployAccountTransactionV0(i)
	case starknetRPC.DeployAccountTxnV3:
		out.Transaction = convertDeployAccountTransactionV3(i)
	default:
		panic(fmt.Errorf("unknown transaction type %T", in))
	}
}

func convertAndSetReceipt(in starknetRPC.TransactionReceipt) *pbstarknet.TransactionReceipt {
	out := &pbstarknet.TransactionReceipt{}
	switch r := in.(type) {
	case starknetRPC.InvokeTransactionReceipt:
		// nothing to do
		out = createCommonTransactionReceipt(starknetRPC.CommonTransactionReceipt(r))
	case starknetRPC.DeclareTransactionReceipt:
		// nothing to do
		out = createCommonTransactionReceipt(starknetRPC.CommonTransactionReceipt(r))
	case starknetRPC.L1HandlerTransactionReceipt:
		out = createCommonTransactionReceipt(r.CommonTransactionReceipt)
		out.MessageHash = string(r.MessageHash)
	case starknetRPC.DeployTransactionReceipt:
		out = createCommonTransactionReceipt(r.CommonTransactionReceipt)
		out.ContractAddress = r.ContractAddress.String()
	case starknetRPC.DeployAccountTransactionReceipt:
		out = createCommonTransactionReceipt(r.CommonTransactionReceipt)
		out.ContractAddress = r.ContractAddress.String()
	default:
		panic(fmt.Errorf("unknown receipt type %T", in))
	}
	return out
}

func createCommonTransactionReceipt(common starknetRPC.CommonTransactionReceipt) *pbstarknet.TransactionReceipt {
	return &pbstarknet.TransactionReceipt{
		Type:            string(common.Type),
		TransactionHash: common.TransactionHash.String(),
		ActualFee: &pbstarknet.ActualFee{
			Amount: common.ActualFee.Amount.String(),
			Unit:   string(common.ActualFee.Unit),
		},
		ExecutionStatus:    common.ExecutionStatus.String(),
		FinalityStatus:     common.FinalityStatus.String(),
		MessagesSent:       convertMessageSent(common.MessagesSent),
		RevertReason:       common.RevertReason,
		Events:             convertEvents(common.Events),
		ExecutionResources: convertExecutionResources(common.ExecutionResources),
	}
}

func convertExecutionResources(r starknetRPC.ExecutionResources) *pbstarknet.ExecutionResources {
	return &pbstarknet.ExecutionResources{
		DataAvailability:              convertDataAvailability(r.DataAvailability),
		Steps:                         uint64(r.Steps),
		MemoryHoles:                   uint64(r.MemoryHoles),
		RangeCheckBuiltinApplications: uint64(r.RangeCheckApps),
		PedersenBuiltinApplications:   uint64(r.PedersenApps),
		PoseidonBuiltinApplications:   uint64(r.PoseidonApps),
		EcOpBuiltinApplications:       uint64(r.ECOPApps),
		EcdsaBuiltinApplications:      uint64(r.ECDSAApps),
		BitwiseBuiltinApplications:    uint64(r.BitwiseApps),
		KeccakBuiltinApplications:     uint64(r.KeccakApps),
		SegmentArenaBuiltin:           uint64(r.SegmentArenaBuiltin),
	}
}

func convertDataAvailability(a starknetRPC.DataAvailability) *pbstarknet.DataAvailability {
	return &pbstarknet.DataAvailability{
		L1DataGas: uint64(a.L1DataGas),
		L1Gas:     uint64(a.L1Gas),
	}
}

func convertEvents(events []starknetRPC.Event) []*pbstarknet.Event {
	out := make([]*pbstarknet.Event, len(events))

	for i, e := range events {
		out[i] = &pbstarknet.Event{
			FromAddress: e.FromAddress.String(),
			Keys:        convertFeltArray(e.Keys),
			Data:        convertFeltArray(e.Data),
		}
	}

	return out
}

func convertMessageSent(msg []starknetRPC.MsgToL1) []*pbstarknet.MessagesSent {
	out := make([]*pbstarknet.MessagesSent, len(msg))

	for i, m := range msg {
		out[i] = &pbstarknet.MessagesSent{
			FromAddress: m.FromAddress.String(),
			ToAddress:   m.ToAddress.String(),
			Payload:     convertFeltArray(m.Payload),
		}
	}

	return out

}

func convertInvokeTransactionV0(tx starknetRPC.InvokeTxnV0) *pbstarknet.TransactionWithReceipt_InvokeTransactionV0 {
	return &pbstarknet.TransactionWithReceipt_InvokeTransactionV0{
		InvokeTransactionV0: &pbstarknet.InvokeTransactionV0{
			Type:               string(tx.GetType()),
			MaxFee:             tx.MaxFee.String(),
			Version:            string(tx.Version),
			Signature:          convertFeltArray(tx.Signature),
			ContractAddress:    tx.ContractAddress.String(),
			EntryPointSelector: tx.EntryPointSelector.String(),
			Calldata:           convertFeltArray(tx.Calldata),
		},
	}
}

func convertInvokeTransactionV1(tx starknetRPC.InvokeTxnV1) *pbstarknet.TransactionWithReceipt_InvokeTransactionV1 {
	return &pbstarknet.TransactionWithReceipt_InvokeTransactionV1{
		InvokeTransactionV1: &pbstarknet.InvokeTransactionV1{
			Type:          string(tx.GetType()),
			SenderAddress: tx.SenderAddress.String(),
			Calldata:      convertFeltArray(tx.Calldata),
			MaxFee:        tx.MaxFee.String(),
			Version:       string(tx.Version),
			Signature:     convertFeltArray(tx.Signature),
			Nonce:         tx.Nonce.String(),
		},
	}
}

func convertInvokeTransactionV3(tx starknetRPC.InvokeTxnV3) *pbstarknet.TransactionWithReceipt_InvokeTransactionV3 {
	return &pbstarknet.TransactionWithReceipt_InvokeTransactionV3{
		InvokeTransactionV3: &pbstarknet.InvokeTransactionV3{
			Type:                      string(tx.GetType()),
			SenderAddress:             tx.SenderAddress.String(),
			Calldata:                  convertFeltArray(tx.Calldata),
			Version:                   string(tx.Version),
			Signature:                 convertFeltArray(tx.Signature),
			Nonce:                     tx.Nonce.String(),
			ResourceBounds:            convertResourceBounds(tx.ResourceBounds),
			Tip:                       string(tx.Tip),
			PaymasterData:             convertFeltArray(tx.PayMasterData),
			AccountDeploymentData:     convertFeltArray(tx.AccountDeploymentData),
			NonceDataAvailabilityMode: string(tx.NonceDataMode),
			FeeDataAvailabilityMode:   string(tx.FeeMode),
		},
	}
}

func convertL1HandlerTransaction(tx starknetRPC.L1HandlerTxn) *pbstarknet.TransactionWithReceipt_L1HandlerTransaction {
	return &pbstarknet.TransactionWithReceipt_L1HandlerTransaction{
		L1HandlerTransaction: &pbstarknet.L1HandlerTransaction{
			Version:            string(tx.Version),
			Type:               string(tx.GetType()),
			Nonce:              tx.Nonce,
			ContractAddress:    tx.ContractAddress.String(),
			EntryPointSelector: tx.EntryPointSelector.String(),
			Calldata:           convertFeltArray(tx.Calldata),
		},
	}
}

func convertDeclareTransactionV0(tx starknetRPC.DeclareTxnV0) *pbstarknet.TransactionWithReceipt_DeclareTransactionV0 {
	return &pbstarknet.TransactionWithReceipt_DeclareTransactionV0{
		DeclareTransactionV0: &pbstarknet.DeclareTransactionV0{
			Type:          string(tx.GetType()),
			SenderAddress: tx.SenderAddress.String(),
			MaxFee:        tx.MaxFee.String(),
			Version:       string(tx.Version),
			Signature:     convertFeltArray(tx.Signature),
			ClassHash:     tx.ClassHash.String(),
		},
	}
}

func convertDeclareTransactionV1(tx starknetRPC.DeclareTxnV1) *pbstarknet.TransactionWithReceipt_DeclareTransactionV1 {
	return &pbstarknet.TransactionWithReceipt_DeclareTransactionV1{
		DeclareTransactionV1: &pbstarknet.DeclareTransactionV1{
			Type:          string(tx.GetType()),
			SenderAddress: tx.SenderAddress.String(),
			MaxFee:        tx.MaxFee.String(),
			Version:       string(tx.Version),
			Signature:     convertFeltArray(tx.Signature),
			Nonce:         tx.Nonce.String(),
			ClassHash:     tx.ClassHash.String(),
		},
	}
}

func convertDeclareTransactionV2(tx starknetRPC.DeclareTxnV2) *pbstarknet.TransactionWithReceipt_DeclareTransactionV2 {
	return &pbstarknet.TransactionWithReceipt_DeclareTransactionV2{
		DeclareTransactionV2: &pbstarknet.DeclareTransactionV2{
			Type:          string(tx.GetType()),
			SenderAddress: tx.SenderAddress.String(),
			MaxFee:        tx.MaxFee.String(),
			Version:       string(tx.Version),
			Signature:     convertFeltArray(tx.Signature),
			Nonce:         tx.Nonce.String(),
			ClassHash:     tx.ClassHash.String(),
		},
	}
}
func convertDeclareTransactionV3(tx starknetRPC.DeclareTxnV3) *pbstarknet.TransactionWithReceipt_DeclareTransactionV3 {
	return &pbstarknet.TransactionWithReceipt_DeclareTransactionV3{
		DeclareTransactionV3: &pbstarknet.DeclareTransactionV3{
			Type:                      string(tx.GetType()),
			SenderAddress:             tx.SenderAddress.String(),
			CompiledClassHash:         tx.CompiledClassHash.String(),
			Version:                   string(tx.Version),
			Signature:                 convertFeltArray(tx.Signature),
			Nonce:                     tx.Nonce.String(),
			ClassHash:                 tx.ClassHash.String(),
			ResourceBounds:            convertResourceBounds(tx.ResourceBounds),
			Tip:                       string(tx.Tip),
			PaymasterData:             convertFeltArray(tx.PayMasterData),
			AccountDeploymentData:     convertFeltArray(tx.AccountDeploymentData),
			NonceDataAvailabilityMode: string(tx.NonceDataMode),
			FeeDataAvailabilityMode:   string(tx.FeeMode),
		},
	}
}

func convertDeployTransactionV0(tx starknetRPC.DeployTxn) *pbstarknet.TransactionWithReceipt_DeployTransactionV0 {
	return &pbstarknet.TransactionWithReceipt_DeployTransactionV0{
		DeployTransactionV0: &pbstarknet.DeployTransactionV0{
			Version:             string(tx.Version),
			Type:                string(tx.GetType()),
			ContractAddressSalt: tx.ContractAddressSalt.String(),
			ConstructorCalldata: convertFeltArray(tx.ConstructorCalldata),
			ClassHash:           tx.ClassHash.String(),
		},
	}

}

func convertDeployAccountTransactionV0(tx starknetRPC.DeployAccountTxn) *pbstarknet.TransactionWithReceipt_DeployAccountTransactionV1 {
	return &pbstarknet.TransactionWithReceipt_DeployAccountTransactionV1{
		DeployAccountTransactionV1: &pbstarknet.DeployAccountTransactionV1{
			Type:                string(tx.GetType()),
			MaxFee:              tx.MaxFee.String(),
			Version:             string(tx.Version),
			Signature:           convertFeltArray(tx.Signature),
			Nonce:               tx.Nonce.String(),
			ContractAddressSalt: tx.ContractAddressSalt.String(),
			ConstructorCalldata: convertFeltArray(tx.ConstructorCalldata),
			ClassHash:           tx.ClassHash.String(),
		},
	}

}

func convertDeployAccountTransactionV3(tx starknetRPC.DeployAccountTxnV3) *pbstarknet.TransactionWithReceipt_DeployAccountTransactionV3 {
	return &pbstarknet.TransactionWithReceipt_DeployAccountTransactionV3{
		DeployAccountTransactionV3: &pbstarknet.DeployAccountTransactionV3{
			Type:                      string(tx.GetType()),
			Version:                   string(tx.Version),
			Signature:                 convertFeltArray(tx.Signature),
			Nonce:                     tx.Nonce.String(),
			ContractAddressSalt:       tx.ContractAddressSalt.String(),
			ClassHash:                 tx.ClassHash.String(),
			ResourceBounds:            convertResourceBounds(tx.ResourceBounds),
			Tip:                       string(tx.Tip),
			PaymasterData:             convertFeltArray(tx.PayMasterData),
			NonceDataAvailabilityMode: string(tx.NonceDataMode),
			FeeDataAvailabilityMode:   string(tx.FeeMode),
		},
	}
}

func convertFeltArray(in []*felt.Felt) []string {
	var out []string
	for _, f := range in {
		out = append(out, f.String())
	}
	return out
}

func convertResourceBounds(in starknetRPC.ResourceBoundsMapping) *pbstarknet.ResourceBounds {
	return &pbstarknet.ResourceBounds{
		L1Gas: &pbstarknet.Resource{
			MaxAmount:       string(in.L1Gas.MaxAmount),
			MaxPricePerUnit: string(in.L1Gas.MaxPricePerUnit),
		},
		L2Gas: &pbstarknet.Resource{
			MaxAmount:       string(in.L2Gas.MaxAmount),
			MaxPricePerUnit: string(in.L2Gas.MaxPricePerUnit),
		},
	}
}
