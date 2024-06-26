package rpc

import (
	"context"
	"fmt"
	"strconv"
	"time"

	pbstarknet "firehose-starknet/pb/sf/starknet/type/v1"

	"github.com/NethermindEth/juno/core/felt"
	snRPC "github.com/NethermindEth/starknet.go/rpc"
	"github.com/hashicorp/go-multierror"
	pbbstream "github.com/streamingfast/bstream/pb/sf/bstream/v1"
	"github.com/streamingfast/eth-go"
	goRPC "github.com/streamingfast/eth-go/rpc"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type RPCClient[C any] struct {
	clients []C
}

func NewRPCClient[C any](clients []C) *RPCClient[C] {
	return &RPCClient[C]{clients: clients}
}

func (r *RPCClient[C]) WithClient(ctx context.Context, f func(client C) error) error {
	var errs error
	for _, client := range r.clients {
		err := f(client)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}
		return errs
	}
	return nil
}

type Fetcher struct {
	rpcClients               []*snRPC.Provider
	starkClients             *RPCClient[*goRPC.Client]
	fetchInterval            time.Duration
	latestBlockRetryInterval time.Duration
	logger                   *zap.Logger
	latestBlockNum           uint64

	ethCallLIBParams goRPC.CallParams
}

func NewFetcher(
	rpcClients []*snRPC.Provider,
	starkClients *RPCClient[*goRPC.Client],
	fetchLIBContractAddress string,
	fetchInterval time.Duration,
	latestBlockRetryInterval time.Duration,
	logger *zap.Logger) *Fetcher {
	return &Fetcher{
		rpcClients:               rpcClients,
		starkClients:             starkClients,
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

	//todo: fix receipt
	//todo: track state update
	// u, err := f.rpcClients[0].StateUpdate(ctx, snRPC.BlockID(requestBlockNum))
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

func (f *Fetcher) fetchLIB(ctx context.Context) (uint64, error) {
	var libNum *uint64
	err := f.starkClients.WithClient(ctx, func(client *goRPC.Client) error {
		blockNum, err := client.Call(ctx, f.ethCallLIBParams)
		if err != nil {
			f.logger.Warn("failed to fetch latest block num, trying next client", zap.Error(err))
			return err
		}

		b, err := strconv.ParseUint(blockNum, 10, 64)
		if err != nil {
			return fmt.Errorf("unable to parse block number %s: %w", blockNum, err)
		}
		libNum = &b
		return nil
	})

	if err != nil {
		return 0, err
	}

	if libNum == nil {
		return 0, fmt.Errorf("unable to fetch LIB")
	}

	return *libNum, nil
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

func newEthCallLIBParams(contractAddress string) goRPC.CallParams {
	return goRPC.CallParams{
		Data: eth.MustNewMethodDef("stateBlockNumber() (int256)").NewCall().MustEncode(),
		To:   eth.MustNewAddress(contractAddress),
	}
}

func convertBlock(b *snRPC.BlockWithReceipts) (*pbbstream.Block, error) {
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

func convertL1DAMode(mode snRPC.L1DAMode) pbstarknet.L1_DA_MODE {
	switch mode {
	case snRPC.L1DAModeBlob:
		return pbstarknet.L1_DA_MODE_BLOB
	case snRPC.L1DAModeCalldata:
		return pbstarknet.L1_DA_MODE_CALLDATA
	default:
		panic(fmt.Errorf("unknown L1DAMode %v", mode))
	}
}

func convertL1DataGasPrice(l snRPC.ResourcePrice) *pbstarknet.L1GasPrice {
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
func convertL1GasPrice(l snRPC.ResourcePrice) *pbstarknet.L1GasPrice {
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

func convertTransactionWithReceipt(tx snRPC.TransactionWithReceipt) (*pbstarknet.TransactionWithReceipt, error) {
	t := &pbstarknet.TransactionWithReceipt{}
	convertAndSetTransaction(t, tx.Transaction.Transaction)

	t.Receipt = convertAndSetReceipt(tx.Receipt.TransactionReceipt)

	return t, nil
}

func convertAndSetTransaction(out *pbstarknet.TransactionWithReceipt, in snRPC.Transaction) {
	switch i := in.(type) {
	case snRPC.InvokeTxnV0:
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
		panic(fmt.Errorf("unknown transaction type %T", in))
	}
	return
}

func convertAndSetReceipt(in snRPC.TransactionReceipt) *pbstarknet.TransactionReceipt {
	common := in.(snRPC.CommonTransactionReceipt)
	out := &pbstarknet.TransactionReceipt{
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

	switch r := in.(type) {
	case snRPC.InvokeTransactionReceipt, snRPC.DeclareTransactionReceipt:
		// nothing to do
	case snRPC.L1HandlerTransactionReceipt:
		out.MessageHash = string(r.MessageHash)
	case snRPC.DeployTransactionReceipt:
		out.ContractAddress = r.ContractAddress.String()
	case snRPC.DeployAccountTransactionReceipt:
		out.ContractAddress = r.ContractAddress.String()
	default:
		panic(fmt.Errorf("unknown receipt type %T", in))
	}
	return out
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

func convertInvokeTransactionV0(tx snRPC.InvokeTxnV0) *pbstarknet.TransactionWithReceipt_InvokeTransactionV0 {
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

func convertInvokeTransactionV1(tx snRPC.InvokeTxnV1) *pbstarknet.TransactionWithReceipt_InvokeTransactionV1 {
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

func convertInvokeTransactionV3(tx snRPC.InvokeTxnV3) *pbstarknet.TransactionWithReceipt_InvokeTransactionV3 {
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

func convertDeclareTransactionV0(tx snRPC.DeclareTxnV0) *pbstarknet.TransactionWithReceipt_DeclareTransactionV0 {
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

func convertDeclareTransactionV1(tx snRPC.DeclareTxnV1) *pbstarknet.TransactionWithReceipt_DeclareTransactionV1 {
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

func convertDeclareTransactionV2(tx snRPC.DeclareTxnV2) *pbstarknet.TransactionWithReceipt_DeclareTransactionV2 {
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
func convertDeclareTransactionV3(tx snRPC.DeclareTxnV3) *pbstarknet.TransactionWithReceipt_DeclareTransactionV3 {
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

func convertDeployTransactionV0(tx snRPC.DeployTxn) *pbstarknet.TransactionWithReceipt_DeployTransactionV0 {
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

func convertDeployAccountTransactionV0(tx snRPC.DeployAccountTxn) *pbstarknet.TransactionWithReceipt_DeployAccountTransactionV1 {
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

func convertDeployAccountTransactionV3(tx snRPC.DeployAccountTxnV3) *pbstarknet.TransactionWithReceipt_DeployAccountTransactionV3 {
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
