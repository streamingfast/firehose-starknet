package rpc

import (
	"context"
	"fmt"
	"time"

	pbstarknet "firehose-starknet/pb/sf/starknet/type/v1"

	"github.com/NethermindEth/juno/core/felt"
	snRPC "github.com/NethermindEth/starknet.go/rpc"
	"github.com/hashicorp/go-multierror"
	pbbstream "github.com/streamingfast/bstream/pb/sf/bstream/v1"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Fetcher struct {
	rpcClients               []*snRPC.Provider
	fetchInterval            time.Duration
	latestBlockRetryInterval time.Duration
	logger                   *zap.Logger
	latestBlockNum           uint64
	root                     string
	rootNNumber              uint64
}

func NewFetcher(rpcClients []*snRPC.Provider, fetchInterval time.Duration, latestBlockRetryInterval time.Duration, logger *zap.Logger) *Fetcher {
	return &Fetcher{
		rpcClients:               rpcClients,
		fetchInterval:            fetchInterval,
		latestBlockRetryInterval: latestBlockRetryInterval,
		logger:                   logger,
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
	blockWithReceipts, err := f.fetchBlock(ctx, requestBlockNum)
	if err != nil {
		return nil, false, fmt.Errorf("fetching block %d: %w", requestBlockNum, err)
	}

	if f.root != blockWithReceipts.NewRoot.String() {
		f.root = blockWithReceipts.NewRoot.String()
		rootNumber, err := f.fetchBlockNumber(ctx, blockWithReceipts.NewRoot)
		if err != nil {
			return nil, false, fmt.Errorf("fetching block number for root %s: %w", blockWithReceipts.NewRoot.String(), err)
		}
		f.rootNNumber = rootNumber
	}

	f.logger.Info("converting block", zap.Uint64("block_num", requestBlockNum))
	bstreamBlock, err := convertBlock(blockWithReceipts)
	if err != nil {
		return nil, false, fmt.Errorf("converting block %d from rpc response: %w", requestBlockNum, err)
	}

	return bstreamBlock, false, nil

}

func (f *Fetcher) fetchBlockNumber(ctx context.Context, blockHash *felt.Felt) (uint64, error) {
	var errs error
	for _, rpcClient := range f.rpcClients {
		i, err := rpcClient.BlockWithTxHashes(ctx, snRPC.WithBlockHash(blockHash))
		if err != nil {
			f.logger.Warn("failed to fetch latest block num, trying next client", zap.Error(err))
			errs = multierror.Append(errs, err)
			continue
		}
		return i.(*snRPC.BlockTxHashes).BlockNumber, nil
	}

	return 0, errs
}

func (f *Fetcher) fetchLatestBlockNum(ctx context.Context) (uint64, error) {
	var errs error
	for _, rpcClient := range f.rpcClients {
		blockNum, err := rpcClient.BlockNumber(ctx)
		if err != nil {
			f.logger.Warn("failed to fetch latest block num, trying next client", zap.Error(err))
			errs = multierror.Append(errs, err)
			continue
		}
		return blockNum, nil
	}

	return 0, errs
}

func (f *Fetcher) fetchBlock(ctx context.Context, requestBlockNum uint64) (*snRPC.BlockWithReceipts, error) {
	var errs error
	for _, rpcClient := range f.rpcClients {
		i, err := rpcClient.BlockWithReceipts(ctx, snRPC.WithBlockNumber(requestBlockNum))
		if err != nil {
			f.logger.Warn("failed to fetch block from rpc", zap.Uint64("block_num", requestBlockNum), zap.Error(err))
			errs = multierror.Append(errs, err)
			continue
		}

		block := i.(*snRPC.BlockWithReceipts)

		return block, nil
	}

	return nil, errs
}

func convertBlock(b *snRPC.BlockWithReceipts) (*pbbstream.Block, error) {
	block := &pbstarknet.Block{
		BlockHash:        b.BlockHash.String(),
		BlockNumber:      b.BlockNumber,
		L1DaMode:         b.L1DAMode.String(),
		NewRoot:          b.NewRoot.String(),
		ParentHash:       b.ParentHash.String(),
		SequencerAddress: b.SequencerAddress.String(),
		StarknetVersion:  b.StarknetVersion,
		Status:           string(b.BlockStatus),
		Timestamp:        b.Timestamp,
		L1DataGasPrice: &pbstarknet.L1DataGasPrice{
			PriceInFri: b.L1DataGasPrice.PriceInFRI.String(),
			PriceInWei: b.L1DataGasPrice.PriceInWei.String(),
		},
		L1GasPrice: &pbstarknet.L1GasPrice{
			PriceInFri: b.L1GasPrice.PriceInFRI.String(),
			PriceInWei: b.L1GasPrice.PriceInWei.String(),
		},
	}

	var transactions []*pbstarknet.TransactionWithReceipt
	for _, tx := range b.Transactions {
		t, err := convertTransactionWithReceipt(tx)
		if err != nil {
			return nil, fmt.Errorf("converting transaction: %w", err)
		}
		transactions = append(transactions, t)
	}

	anyBlock, err := anypb.New(block)
	if err != nil {
		return nil, fmt.Errorf("unable to create anypb: %w", err)
	}

	bstreamBlock := &pbbstream.Block{
		Number:    block.BlockNumber,
		Id:        block.BlockHash,
		ParentId:  block.ParentHash,
		Timestamp: timestamppb.New(time.Unix(int64(block.Timestamp), 0)),
		LibNum:    block.BlockNumber,
		ParentNum: block.BlockNumber - 1,
		Payload:   anyBlock,
	}

	return bstreamBlock, nil
}

func convertTransactionWithReceipt(tx snRPC.TransactionWithReceipt) (*pbstarknet.TransactionWithReceipt, error) {
	t := &pbstarknet.TransactionWithReceipt{}
	convertAndSetTransaction(t, tx.Transaction)

	convertAndSetReceipt(t, tx.Receipt)

	return t, nil
}

func convertAndSetTransaction(out *pbstarknet.TransactionWithReceipt, in snRPC.Transaction) {
	switch i := in.(type) {
	case *snRPC.InvokeTxnV0:
		out.Transaction = convertInvokeTransactionV0(i)
	case snRPC.InvokeTxnV1:
		out.Transaction = convertInvokeTransactionV1(i)
	case snRPC.InvokeTxnV3:
		out.Transaction = convertInvokeTransactionV3(i)
	case snRPC.L1HandlerTxn:
		out.Transaction = convertL1HandlerTransaction(i)
	case snRPC.DeclareTxnV0:
		out.Transaction = convertDeclareTransactionV0(i)
	case snRPC.DeclareTxnV1:
		out.Transaction = convertDeclareTransactionV1(i)
	case snRPC.DeclareTxnV2:
		out.Transaction = convertDeclareTransactionV2(i)
	case snRPC.DeclareTxnV3:
		out.Transaction = convertDeclareTransactionV3(i)
	case snRPC.DeployTxn:
		out.Transaction = convertDeployTransactionV0(i)
	case snRPC.DeployAccountTxn:
		out.Transaction = convertDeployAccountTransactionV0(i)
	case snRPC.DeployAccountTxnV3:
		out.Transaction = convertDeployAccountTransactionV3(i)
	default:
		panic(fmt.Errorf("unknown transaction type %q", in.GetType()))
	}
	return
}

func convertAndSetReceipt(out *pbstarknet.TransactionWithReceipt, in snRPC.TransactionReceipt) {
	switch r := in.(type) {
	case *snRPC.InvokeTransactionReceipt:
		out.Receipt = convertInvokeTransactionReceipt(r)
	case *snRPC.L1HandlerTransactionReceipt:
		out.Receipt = convertL1HandlerTransactionReceipt(r)
	case *snRPC.DeclareTransactionReceipt:
		out.Receipt = convertDeclareTransactionReceipt(r)
	case *snRPC.DeployTransactionReceipt:
		out.Receipt = convertDeployTransactionReceipt(r)
	case *snRPC.DeployAccountTransactionReceipt:
		out.Receipt = convertDeployAccountTransactionReceipt(r)
	default:
		panic(fmt.Errorf("unknown receipt type %v", in))
	}
	return
}

func convertInvokeTransactionReceipt(r *snRPC.InvokeTransactionReceipt) *pbstarknet.TransactionWithReceipt_InvokeTransactionReceipt {
	return &pbstarknet.TransactionWithReceipt_InvokeTransactionReceipt{
		InvokeTransactionReceipt: &pbstarknet.InvokeTransactionReceipt{
			Type:            string(r.Type),
			TransactionHash: r.TransactionHash.String(),
			ActualFee: &pbstarknet.ActualFee{
				Amount: r.ActualFee.Amount.String(),
				Unit:   string(r.ActualFee.Unit),
			},
			ExecutionStatus:    string(r.ExecutionStatus),
			FinalityStatus:     string(r.FinalityStatus),
			MessagesSent:       convertMessageSent(r.MessagesSent),
			RevertReason:       r.RevertReason,
			Events:             convertEvents(r.Events),
			ExecutionResources: convertExecutionResources(r.ExecutionResources),
		},
	}

}
func convertL1HandlerTransactionReceipt(r *snRPC.L1HandlerTransactionReceipt) *pbstarknet.TransactionWithReceipt_L1HandlerTransactionReceipt {
	return &pbstarknet.TransactionWithReceipt_L1HandlerTransactionReceipt{
		L1HandlerTransactionReceipt: &pbstarknet.L1HandlerTransactionReceipt{
			Type:            string(r.Type),
			MessageHash:     r.MessagesSent,????
			TransactionHash: r.TransactionHash.String(),
			ActualFee: &pbstarknet.ActualFee{
				Amount: r.ActualFee.Amount.String(),
				Unit:   string(r.ActualFee.Unit),
			},
			ExecutionStatus:    r.ExecutionStatus.String(),
			FinalityStatus:     r.FinalityStatus.String(),
			MessagesSent:       convertMessageSent(r.MessagesSent),
			RevertReason:       r.RevertReason,
			Events:             convertEvents(r.Events),
			ExecutionResources: convertExecutionResources(r.ExecutionResources),
		},
	}
}

func convertDeclareTransactionReceipt(r *snRPC.DeclareTransactionReceipt) *pbstarknet.TransactionWithReceipt_DeclareTransactionReceipt {
	return &pbstarknet.TransactionWithReceipt_DeclareTransactionReceipt{
		DeclareTransactionReceipt: &pbstarknet.DeclareTransactionReceipt{
			Type:               string(r.Type),
			TransactionHash:    r.TransactionHash.String(),
			ActualFee: &pbstarknet.ActualFee{
				Amount: r.ActualFee.Amount.String(),
				Unit:   string(r.ActualFee.Unit),
			},
			ExecutionStatus:    string(r.ExecutionStatus),
			FinalityStatus:     string(r.FinalityStatus),
			MessagesSent:       convertMessageSent(r.MessagesSent),
			RevertReason:       r.RevertReason,
			Events:             convertEvents(r.Events),
			ExecutionResources: convertExecutionResources(r.ExecutionResources),
		},
	}
}

func convertDeployTransactionReceipt(r *snRPC.DeployTransactionReceipt) *pbstarknet.TransactionWithReceipt_DeployTransactionReceipt {
	return &pbstarknet.TransactionWithReceipt_DeployTransactionReceipt{
		DeployTransactionReceipt: &pbstarknet.DeployTransactionReceipt{
			Type:               string(r.Type),
			ContractAddress:    r.ContractAddress.String(),
			TransactionHash:    r.TransactionHash.String(),
			ActualFee: &pbstarknet.ActualFee{
				Amount: r.ActualFee.Amount.String(),
				Unit:   string(r.ActualFee.Unit),
			},
			ExecutionStatus:    string(r.ExecutionStatus),
			FinalityStatus:     string(r.FinalityStatus),
			MessagesSent:       convertMessageSent(r.MessagesSent),
			RevertReason:       r.RevertReason,
			Events:             convertEvents(r.Events),
			ExecutionResources: convertExecutionResources(r.ExecutionResources),
		},
	}
}

func convertDeployAccountTransactionReceipt(r *snRPC.DeployAccountTransactionReceipt) *pbstarknet.TransactionWithReceipt_DeployAccountTransactionReceipt{
	return &pbstarknet.TransactionWithReceipt_DeployAccountTransactionReceipt{
		DeployAccountTransactionReceipt: &pbstarknet.DeployAccountTransactionReceipt{
			Type:               string(r.Type),
			ContractAddress:    r.ContractAddress.String(),
			TransactionHash:    r.TransactionHash.String(),
			ActualFee:          nil,
			ExecutionStatus:    string(r.ExecutionStatus),
			FinalityStatus:     string(r.FinalityStatus),
			MessagesSent:       convertMessageSent(r.MessagesSent),
			RevertReason:       r.RevertReason,
			Events:             convertEvents(r.Events),
			ExecutionResources: convertExecutionResources(r.ExecutionResources),
		},
	}
}

func convertExecutionResources(r snRPC.ExecutionResources) *pbstarknet.ExecutionResources {
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

func convertDataAvailability(a snRPC.DataAvailability) *pbstarknet.DataAvailability {
	return &pbstarknet.DataAvailability{
		L1DataGas: uint64(a.L1DataGas),
		L1Gas:     uint64(a.L1Gas),
	}
}

func convertEvents(events []snRPC.Event) []*pbstarknet.Event {
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

func convertMessageSent(msg []snRPC.MsgToL1) []*pbstarknet.MessagesSent {
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

func convertInvokeTransactionV0(tx *snRPC.InvokeTxnV0) *pbstarknet.TransactionWithReceipt_InvokeTransaction_V0 {
	return &pbstarknet.TransactionWithReceipt_InvokeTransaction_V0{
		InvokeTransaction_V0: &pbstarknet.InvokeTransactionV0{
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

func convertInvokeTransactionV1(tx snRPC.InvokeTxnV1) *pbstarknet.TransactionWithReceipt_InvokeTransaction_V1 {
	return &pbstarknet.TransactionWithReceipt_InvokeTransaction_V1{
		InvokeTransaction_V1: &pbstarknet.InvokeTransactionV1{
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

func convertInvokeTransactionV3(tx snRPC.InvokeTxnV3) *pbstarknet.TransactionWithReceipt_InvokeTransaction_V3 {
	return &pbstarknet.TransactionWithReceipt_InvokeTransaction_V3{
		InvokeTransaction_V3: &pbstarknet.InvokeTransactionV3{
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

func convertL1HandlerTransaction(tx snRPC.L1HandlerTxn) *pbstarknet.TransactionWithReceipt_L1HandlerTransaction {
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

func convertDeclareTransactionV0(tx snRPC.DeclareTxnV0) *pbstarknet.TransactionWithReceipt_DeclareTransaction_V0 {
	return &pbstarknet.TransactionWithReceipt_DeclareTransaction_V0{
		DeclareTransaction_V0: &pbstarknet.DeclareTransactionV0{
			Type:          string(tx.GetType()),
			SenderAddress: tx.SenderAddress.String(),
			MaxFee:        tx.MaxFee.String(),
			Version:       string(tx.Version),
			Signature:     convertFeltArray(tx.Signature),
			ClassHash:     tx.ClassHash.String(),
		},
	}
}

func convertDeclareTransactionV1(tx snRPC.DeclareTxnV1) *pbstarknet.TransactionWithReceipt_DeclareTransaction_V1 {
	return &pbstarknet.TransactionWithReceipt_DeclareTransaction_V1{
		DeclareTransaction_V1: &pbstarknet.DeclareTransactionV1{
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

func convertDeclareTransactionV2(tx snRPC.DeclareTxnV2) *pbstarknet.TransactionWithReceipt_DeclareTransaction_V2 {
	return &pbstarknet.TransactionWithReceipt_DeclareTransaction_V2{
		DeclareTransaction_V2: &pbstarknet.DeclareTransactionV2{
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
func convertDeclareTransactionV3(tx snRPC.DeclareTxnV3) *pbstarknet.TransactionWithReceipt_DeclareTransaction_V3 {
	return &pbstarknet.TransactionWithReceipt_DeclareTransaction_V3{
		DeclareTransaction_V3: &pbstarknet.DeclareTransactionV3{
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

func convertDeployTransactionV0(tx snRPC.DeployTxn) *pbstarknet.TransactionWithReceipt_DeployTransaction_V0 {
	return &pbstarknet.TransactionWithReceipt_DeployTransaction_V0{
		DeployTransaction_V0: &pbstarknet.DeployTransactionV0{
			Version:             string(tx.Version),
			Type:                string(tx.GetType()),
			ContractAddressSalt: tx.ContractAddressSalt.String(),
			ConstructorCalldata: convertFeltArray(tx.ConstructorCalldata),
			ClassHash:           tx.ClassHash.String(),
		},
	}

}

func convertDeployAccountTransactionV0(tx snRPC.DeployAccountTxn) *pbstarknet.TransactionWithReceipt_DeployAccountTransaction_V0 {
	return &pbstarknet.TransactionWithReceipt_DeployAccountTransaction_V0{
		DeployAccountTransaction_V0: &pbstarknet.DeployAccountTransactionV0{
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

func convertDeployAccountTransactionV3(tx snRPC.DeployAccountTxnV3) *pbstarknet.TransactionWithReceipt_DeployAccountTransaction_V3 {
	return &pbstarknet.TransactionWithReceipt_DeployAccountTransaction_V3{
		DeployAccountTransaction_V3: &pbstarknet.DeployAccountTransactionV3{
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

func convertResourceBounds(in snRPC.ResourceBoundsMapping) *pbstarknet.ResourceBounds {
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
