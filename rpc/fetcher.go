package rpc

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/NethermindEth/juno/core/felt"
	starknetRPC "github.com/NethermindEth/starknet.go/rpc"
	"github.com/ethereum/go-ethereum/common/hexutil"
	pbbstream "github.com/streamingfast/bstream/pb/sf/bstream/v1"
	"github.com/streamingfast/eth-go"
	ethRPC "github.com/streamingfast/eth-go/rpc"
	"github.com/streamingfast/firehose-core/blockpoller"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	pbstarknet "github.com/streamingfast/firehose-starknet/pb/sf/starknet/type/v1"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ blockpoller.BlockFetcher[*starknetRPC.Provider] = (*Fetcher)(nil)

type Fetcher struct {
	ethClients               *firecoreRPC.Clients[*ethRPC.Client]
	fetchInterval            time.Duration
	latestBlockRetryInterval time.Duration
	logger                   *zap.Logger
	latestBlockNum           uint64

	ethCallLIBParams             ethRPC.CallParams
	lastL1AcceptedBlock          uint64
	lastL1AcceptedBlockFetchTime time.Time
}

func NewFetcher(
	ethClient *firecoreRPC.Clients[*ethRPC.Client],
	fetchLIBContractAddress string,
	fetchInterval time.Duration,
	latestBlockRetryInterval time.Duration,
	logger *zap.Logger) *Fetcher {
	return &Fetcher{
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

func (f *Fetcher) Fetch(ctx context.Context, client *starknetRPC.Provider, requestBlockNum uint64) (b *pbbstream.Block, skipped bool, err error) {
	f.logger.Info("fetching block", zap.Uint64("block_num", requestBlockNum))

	sleepDuration := time.Duration(0)
	for f.latestBlockNum < requestBlockNum {
		time.Sleep(sleepDuration)

		f.latestBlockNum, err = f.fetchLatestBlockNum(ctx, client)
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
	blockWithReceipts, err := FetchBlock(ctx, client, requestBlockNum)
	if err != nil {
		return nil, false, fmt.Errorf("fetching block %d: %w", requestBlockNum, err)
	}
	f.logger.Debug("block fetched successfully", zap.Uint64("block_num", requestBlockNum))

	if f.ethClients != nil {
		if f.lastL1AcceptedBlock == 0 || time.Since(f.lastL1AcceptedBlockFetchTime) > time.Minute*5 {
			f.logger.Info("fetching last L1 accepted block")
			lastL1AcceptedBlock, err := f.fetchLastL1AcceptBlock()
			if err != nil {
				return nil, false, fmt.Errorf("fetching LIB: %w", err)
			}
			f.lastL1AcceptedBlock = lastL1AcceptedBlock
			f.lastL1AcceptedBlockFetchTime = time.Now()
		}
	}

	stateUpdate, err := FetchStateUpdate(ctx, client, requestBlockNum)
	if err != nil {
		return nil, false, fmt.Errorf("fetching state update: %w", err)
	}

	f.logger.Info("converting block", zap.Uint64("block_num", requestBlockNum))
	bstreamBlock, err := convertBlock(blockWithReceipts, stateUpdate, f.lastL1AcceptedBlock)
	if err != nil {
		return nil, false, fmt.Errorf("converting block %d from rpc response: %w", requestBlockNum, err)
	}

	return bstreamBlock, false, nil

}

func FetchStateUpdate(ctx context.Context, client *starknetRPC.Provider, blockNum uint64) (*starknetRPC.StateUpdateOutput, error) {
	updates, err := client.StateUpdate(ctx, starknetRPC.WithBlockNumber(blockNum))
	if err != nil {
		return nil, fmt.Errorf("fetching state update: %w", err)
	}

	//SORT PARTY

	slices.SortFunc(updates.StateDiff.StorageDiffs, func(a, b starknetRPC.ContractStorageDiffItem) int {
		return strings.Compare(a.Address.String(), b.Address.String())
	})
	for _, d := range updates.StateDiff.StorageDiffs {
		slices.SortFunc(d.StorageEntries, func(a, b starknetRPC.StorageEntry) int {
			return strings.Compare(a.Key.String(), b.Key.String())
		})
	}

	slices.SortFunc(updates.StateDiff.DeprecatedDeclaredClasses, func(a, b *felt.Felt) int {
		return strings.Compare(a.String(), b.String())
	})

	slices.SortFunc(updates.StateDiff.DeclaredClasses, func(a, b starknetRPC.DeclaredClassesItem) int {
		return strings.Compare(a.ClassHash.String(), b.ClassHash.String())
	})

	slices.SortFunc(updates.StateDiff.DeployedContracts, func(a, b starknetRPC.DeployedContractItem) int {
		return strings.Compare(a.Address.String(), b.Address.String())
	})

	slices.SortFunc(updates.StateDiff.ReplacedClasses, func(a, b starknetRPC.ReplacedClassesItem) int {
		return strings.Compare(a.ClassHash.String(), b.ClassHash.String())
	})

	slices.SortFunc(updates.StateDiff.Nonces, func(a, b starknetRPC.ContractNonce) int {
		return strings.Compare(a.ContractAddress.String(), b.ContractAddress.String())
	})

	return updates, err
}

func (f *Fetcher) fetchBlockNumber(ctx context.Context, client *starknetRPC.Provider, blockHash *felt.Felt) (uint64, error) {
	bn, err := client.BlockWithTxHashes(ctx, starknetRPC.WithBlockHash(blockHash))
	if err != nil {
		return 0, fmt.Errorf("unable to fetch block number: %w", err)
	}
	return bn.(*starknetRPC.BlockTxHashes).BlockNumber, nil
}

func (f *Fetcher) fetchLatestBlockNum(ctx context.Context, client *starknetRPC.Provider) (uint64, error) {
	blockNum, err := client.BlockNumber(ctx)
	if err != nil {
		return 0, fmt.Errorf("unable to fetch latest block num: %w", err)
	}
	return blockNum, nil

}

func (f *Fetcher) fetchLastL1AcceptBlock() (uint64, error) {
	return firecoreRPC.WithClients(f.ethClients, func(ctx context.Context, client *ethRPC.Client) (uint64, error) {
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

func FetchBlock(ctx context.Context, client *starknetRPC.Provider, requestBlockNum uint64) (*starknetRPC.BlockWithReceipts, error) {
	b, err := client.BlockWithReceipts(ctx, starknetRPC.WithBlockNumber(requestBlockNum))
	if err != nil {
		return nil, fmt.Errorf("unable to fetch block: %w", err)
	}
	return b.(*starknetRPC.BlockWithReceipts), nil
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

func convertBlock(b *starknetRPC.BlockWithReceipts, s *starknetRPC.StateUpdateOutput, lastL1AcceptedBlock uint64) (*pbbstream.Block, error) {
	lib := lastL1AcceptedBlock
	if b.BlockNumber >= lib {
		if b.BlockNumber > 0 {
			lib = b.BlockNumber - 1
		} else {
			lib = 0
		}
	}
	stateUpdate := convertStateUpdate(s)
	block := &pbstarknet.Block{
		BlockHash:        convertFelt(b.BlockHash),
		BlockNumber:      b.BlockNumber,
		L1DaMode:         convertL1DAMode(b.L1DAMode),
		NewRoot:          convertFelt(b.NewRoot),
		ParentHash:       convertFelt(b.ParentHash),
		SequencerAddress: convertFelt(b.SequencerAddress),
		StarknetVersion:  b.StarknetVersion,
		Timestamp:        b.Timestamp,
		L1DataGasPrice:   convertL1DataGasPrice(b.L1DataGasPrice),
		L1GasPrice:       convertL1GasPrice(b.L1GasPrice),
		StateUpdate:      stateUpdate,
	}

	transactions := make([]*pbstarknet.TransactionWithReceipt, 0, len(b.Transactions))
	for _, tx := range b.Transactions {
		t, err := convertTransactionWithReceipt(tx)
		if err != nil {
			return nil, fmt.Errorf("converting transaction: %w", err)
		}
		transactions = append(transactions, t)
	}

	block.Transactions = transactions

	anyBlock, err := anypb.New(block)
	if err != nil {
		return nil, fmt.Errorf("unable to create anypb: %w", err)
	}

	var parentBlockNum uint64
	if block.BlockNumber > 0 {
		parentBlockNum = block.BlockNumber - 1
	}

	libNum := parentBlockNum

	id := &felt.Felt{}
	id = id.SetBytes(block.BlockHash)
	parentId := &felt.Felt{}
	parentId = parentId.SetBytes(block.ParentHash)
	bstreamBlock := &pbbstream.Block{
		Number:    block.BlockNumber,
		Id:        id.String(),
		ParentId:  parentId.String(),
		Timestamp: timestamppb.New(time.Unix(int64(block.Timestamp), 0)),
		LibNum:    libNum,
		ParentNum: parentBlockNum,
		Payload:   anyBlock,
	}

	return bstreamBlock, nil
}

func convertStateUpdate(s *starknetRPC.StateUpdateOutput) *pbstarknet.StateUpdate {
	return &pbstarknet.StateUpdate{
		OldRoot:   convertFelt(s.OldRoot),
		NewRoot:   convertFelt(s.NewRoot),
		StateDiff: convertStateDiff(s.StateDiff),
	}
}

func convertStateDiff(diff starknetRPC.StateDiff) *pbstarknet.StateDiff {
	return &pbstarknet.StateDiff{
		StorageDiffs:              convertStorageDiff(diff.StorageDiffs),
		DeprecatedDeclaredClasses: convertFeltArray(diff.DeprecatedDeclaredClasses),
		DeclaredClasses:           convertDeclaredClasses(diff.DeclaredClasses),
		DeployedContracts:         convertDeployedContracts(diff.DeployedContracts),
		ReplacedClasses:           convertReplacedClasses(diff.ReplacedClasses),
		Nonces:                    convertNonceDiffs(diff.Nonces),
	}
}

func convertNonceDiffs(nonces []starknetRPC.ContractNonce) []*pbstarknet.NonceDiff {
	out := make([]*pbstarknet.NonceDiff, len(nonces))

	for i, n := range nonces {
		out[i] = &pbstarknet.NonceDiff{
			ContractAddress: convertFelt(n.ContractAddress),
			Nonce:           convertFelt(n.Nonce),
		}
	}

	return out
}

func convertReplacedClasses(classes []starknetRPC.ReplacedClassesItem) []*pbstarknet.ReplacedClass {
	out := make([]*pbstarknet.ReplacedClass, len(classes))

	for i, c := range classes {
		out[i] = &pbstarknet.ReplacedClass{
			ContractAddress: convertFelt(c.ContractClass),
			ClassHash:       convertFelt(c.ClassHash),
		}
	}

	return out
}

func convertFelt(f *felt.Felt) []byte {
	if f == nil {
		return nil
	}
	d := f.Bytes()
	return d[:]
}

func convertDeployedContracts(contracts []starknetRPC.DeployedContractItem) []*pbstarknet.DeployedContract {
	out := make([]*pbstarknet.DeployedContract, len(contracts))

	for i, c := range contracts {
		out[i] = &pbstarknet.DeployedContract{
			Address:   convertFelt(c.Address),
			ClassHash: convertFelt(c.ClassHash),
		}
	}

	return out
}

func convertDeclaredClasses(classes []starknetRPC.DeclaredClassesItem) []*pbstarknet.DeclaredClass {
	out := make([]*pbstarknet.DeclaredClass, len(classes))

	for i, c := range classes {
		out[i] = &pbstarknet.DeclaredClass{
			ClassHash:         convertFelt(c.ClassHash),
			CompiledClassHash: convertFelt(c.CompiledClassHash),
		}
	}

	return out
}

func convertStorageDiff(diffs []starknetRPC.ContractStorageDiffItem) []*pbstarknet.ContractStorageDiff {
	out := make([]*pbstarknet.ContractStorageDiff, len(diffs))

	for i, d := range diffs {
		out[i] = &pbstarknet.ContractStorageDiff{
			Address:        convertFelt(d.Address),
			StorageEntries: convertStorageEntries(d.StorageEntries),
		}
	}

	return out
}

func convertStorageEntries(entries []starknetRPC.StorageEntry) []*pbstarknet.StorageEntries {
	out := make([]*pbstarknet.StorageEntries, len(entries))

	for i, e := range entries {
		out[i] = &pbstarknet.StorageEntries{
			Key:   convertFelt(e.Key),
			Value: convertFelt(e.Value),
		}
	}

	return out
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

func convertL1DataGasPrice(l starknetRPC.ResourcePrice) *pbstarknet.ResourcePrice {
	f := &felt.Zero
	if l.PriceInFRI != nil {
		f = l.PriceInFRI
	}
	w := &felt.Zero
	if l.PriceInWei != nil {
		w = l.PriceInWei
	}
	return &pbstarknet.ResourcePrice{
		PriceInFri: convertFelt(f),
		PriceInWei: convertFelt(w),
	}
}
func convertL1GasPrice(l starknetRPC.ResourcePrice) *pbstarknet.ResourcePrice {
	f := &felt.Zero
	if l.PriceInFRI != nil {
		f = l.PriceInFRI
	}
	w := &felt.Zero
	if l.PriceInWei != nil {
		w = l.PriceInWei
	}
	return &pbstarknet.ResourcePrice{
		PriceInFri: convertFelt(f),
		PriceInWei: convertFelt(w),
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
		out.ContractAddress = convertFelt(r.ContractAddress)
	case starknetRPC.DeployAccountTransactionReceipt:
		out = createCommonTransactionReceipt(r.CommonTransactionReceipt)
		out.ContractAddress = convertFelt(r.ContractAddress)
	default:
		panic(fmt.Errorf("unknown receipt type %T", in))
	}
	return out
}

func createCommonTransactionReceipt(common starknetRPC.CommonTransactionReceipt) *pbstarknet.TransactionReceipt {
	return &pbstarknet.TransactionReceipt{
		Type:            convertTransactionType(common.Type),
		TransactionHash: convertFelt(common.TransactionHash),
		ActualFee: &pbstarknet.ActualFee{
			Amount: convertFelt(common.ActualFee.Amount),
			Unit:   string(common.ActualFee.Unit),
		},
		ExecutionStatus:    convertExecutionStature(common.ExecutionStatus),
		MessagesSent:       convertMessageSent(common.MessagesSent),
		RevertReason:       common.RevertReason,
		Events:             convertEvents(common.Events),
		ExecutionResources: convertExecutionResources(common.ExecutionResources),
	}
}

func convertExecutionStature(status starknetRPC.TxnExecutionStatus) pbstarknet.EXECUTION_STATUS {
	switch status {
	case starknetRPC.TxnExecutionStatusSUCCEEDED:
		return pbstarknet.EXECUTION_STATUS_EXECUTION_STATUS_SUCCESS
	case starknetRPC.TxnExecutionStatusREVERTED:
		return pbstarknet.EXECUTION_STATUS_EXECUTION_STATUS_REVERTED
	default:
		panic(fmt.Errorf("unknown execution status %v", status))
	}
}

func convertTransactionType(transactionType starknetRPC.TransactionType) pbstarknet.TRANSACTION_TYPE {
	switch transactionType {
	case starknetRPC.TransactionType_Invoke:
		return pbstarknet.TRANSACTION_TYPE_INVOKE
	case starknetRPC.TransactionType_Declare:
		return pbstarknet.TRANSACTION_TYPE_DECLARE
	case starknetRPC.TransactionType_L1Handler:
		return pbstarknet.TRANSACTION_TYPE_L1_HANDLER
	case starknetRPC.TransactionType_Deploy:
		return pbstarknet.TRANSACTION_TYPE_DEPLOY
	case starknetRPC.TransactionType_DeployAccount:
		return pbstarknet.TRANSACTION_TYPE_DEPLOY_ACCOUNT
	default:
		panic(fmt.Errorf("unknown transaction type %v", transactionType))
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
			FromAddress: convertFelt(e.FromAddress),
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
			FromAddress: convertFelt(m.FromAddress),
			ToAddress:   convertFelt(m.ToAddress),
			Payload:     convertFeltArray(m.Payload),
		}
	}

	return out

}

func convertInvokeTransactionV0(tx starknetRPC.InvokeTxnV0) *pbstarknet.TransactionWithReceipt_InvokeTransactionV0 {
	return &pbstarknet.TransactionWithReceipt_InvokeTransactionV0{
		InvokeTransactionV0: &pbstarknet.InvokeTransactionV0{
			MaxFee:             convertFelt(tx.MaxFee),
			Version:            string(tx.Version),
			Signature:          convertFeltArray(tx.Signature),
			ContractAddress:    convertFelt(tx.ContractAddress),
			EntryPointSelector: convertFelt(tx.EntryPointSelector),
			Calldata:           convertFeltArray(tx.Calldata),
		},
	}
}

func convertInvokeTransactionV1(tx starknetRPC.InvokeTxnV1) *pbstarknet.TransactionWithReceipt_InvokeTransactionV1 {
	return &pbstarknet.TransactionWithReceipt_InvokeTransactionV1{
		InvokeTransactionV1: &pbstarknet.InvokeTransactionV1{
			SenderAddress: convertFelt(tx.SenderAddress),
			Calldata:      convertFeltArray(tx.Calldata),
			MaxFee:        convertFelt(tx.MaxFee),
			Version:       string(tx.Version),
			Signature:     convertFeltArray(tx.Signature),
			Nonce:         convertFelt(tx.Nonce),
		},
	}
}

func convertInvokeTransactionV3(tx starknetRPC.InvokeTxnV3) *pbstarknet.TransactionWithReceipt_InvokeTransactionV3 {
	return &pbstarknet.TransactionWithReceipt_InvokeTransactionV3{
		InvokeTransactionV3: &pbstarknet.InvokeTransactionV3{
			SenderAddress:             convertFelt(tx.SenderAddress),
			Calldata:                  convertFeltArray(tx.Calldata),
			Version:                   string(tx.Version),
			Signature:                 convertFeltArray(tx.Signature),
			Nonce:                     convertFelt(tx.Nonce),
			ResourceBounds:            convertResourceBounds(tx.ResourceBounds),
			Tip:                       mustConvertTip(tx.Tip),
			PaymasterData:             convertFeltArray(tx.PayMasterData),
			AccountDeploymentData:     convertFeltArray(tx.AccountDeploymentData),
			NonceDataAvailabilityMode: convertFeeMode(tx.NonceDataMode),
			FeeDataAvailabilityMode:   convertFeeMode(tx.FeeMode),
		},
	}
}

func mustConvertTip(tip starknetRPC.U64) []byte {
	f := &felt.Felt{}
	f, err := f.SetString(string(tip))
	if err != nil {
		panic(fmt.Errorf("unable to convert tip %v: %w", tip, err))
	}
	return f.Marshal()
}

func convertL1HandlerTransaction(tx starknetRPC.L1HandlerTxn) *pbstarknet.TransactionWithReceipt_L1HandlerTransaction {
	return &pbstarknet.TransactionWithReceipt_L1HandlerTransaction{
		L1HandlerTransaction: &pbstarknet.L1HandlerTransaction{
			Version:            string(tx.Version),
			Nonce:              tx.Nonce,
			ContractAddress:    convertFelt(tx.ContractAddress),
			EntryPointSelector: convertFelt(tx.EntryPointSelector),
			Calldata:           convertFeltArray(tx.Calldata),
		},
	}
}

func convertDeclareTransactionV0(tx starknetRPC.DeclareTxnV0) *pbstarknet.TransactionWithReceipt_DeclareTransactionV0 {
	return &pbstarknet.TransactionWithReceipt_DeclareTransactionV0{
		DeclareTransactionV0: &pbstarknet.DeclareTransactionV0{
			SenderAddress: convertFelt(tx.SenderAddress),
			MaxFee:        convertFelt(tx.MaxFee),
			Version:       string(tx.Version),
			Signature:     convertFeltArray(tx.Signature),
			ClassHash:     convertFelt(tx.ClassHash),
		},
	}
}

func convertDeclareTransactionV1(tx starknetRPC.DeclareTxnV1) *pbstarknet.TransactionWithReceipt_DeclareTransactionV1 {
	return &pbstarknet.TransactionWithReceipt_DeclareTransactionV1{
		DeclareTransactionV1: &pbstarknet.DeclareTransactionV1{
			SenderAddress: convertFelt(tx.SenderAddress),
			MaxFee:        convertFelt(tx.MaxFee),
			Version:       string(tx.Version),
			Signature:     convertFeltArray(tx.Signature),
			Nonce:         convertFelt(tx.Nonce),
			ClassHash:     convertFelt(tx.ClassHash),
		},
	}
}

func convertDeclareTransactionV2(tx starknetRPC.DeclareTxnV2) *pbstarknet.TransactionWithReceipt_DeclareTransactionV2 {
	return &pbstarknet.TransactionWithReceipt_DeclareTransactionV2{
		DeclareTransactionV2: &pbstarknet.DeclareTransactionV2{
			SenderAddress:     convertFelt(tx.SenderAddress),
			CompiledClassHash: convertFelt(tx.CompiledClassHash),
			MaxFee:            convertFelt(tx.MaxFee),
			Version:           string(tx.Version),
			Signature:         convertFeltArray(tx.Signature),
			Nonce:             convertFelt(tx.Nonce),
			ClassHash:         convertFelt(tx.ClassHash),
		},
	}
}
func convertDeclareTransactionV3(tx starknetRPC.DeclareTxnV3) *pbstarknet.TransactionWithReceipt_DeclareTransactionV3 {
	return &pbstarknet.TransactionWithReceipt_DeclareTransactionV3{
		DeclareTransactionV3: &pbstarknet.DeclareTransactionV3{
			SenderAddress:             convertFelt(tx.SenderAddress),
			CompiledClassHash:         convertFelt(tx.CompiledClassHash),
			Version:                   string(tx.Version),
			Signature:                 convertFeltArray(tx.Signature),
			Nonce:                     convertFelt(tx.Nonce),
			ClassHash:                 convertFelt(tx.ClassHash),
			ResourceBounds:            convertResourceBounds(tx.ResourceBounds),
			Tip:                       mustConvertTip(tx.Tip),
			PaymasterData:             convertFeltArray(tx.PayMasterData),
			AccountDeploymentData:     convertFeltArray(tx.AccountDeploymentData),
			NonceDataAvailabilityMode: convertFeeMode(tx.NonceDataMode),
			FeeDataAvailabilityMode:   convertFeeMode(tx.FeeMode),
		},
	}
}

func convertDeployTransactionV0(tx starknetRPC.DeployTxn) *pbstarknet.TransactionWithReceipt_DeployTransactionV0 {
	return &pbstarknet.TransactionWithReceipt_DeployTransactionV0{
		DeployTransactionV0: &pbstarknet.DeployTransactionV0{
			Version:             string(tx.Version),
			ContractAddressSalt: convertFelt(tx.ContractAddressSalt),
			ConstructorCalldata: convertFeltArray(tx.ConstructorCalldata),
			ClassHash:           convertFelt(tx.ClassHash),
		},
	}

}

func convertDeployAccountTransactionV0(tx starknetRPC.DeployAccountTxn) *pbstarknet.TransactionWithReceipt_DeployAccountTransactionV1 {
	return &pbstarknet.TransactionWithReceipt_DeployAccountTransactionV1{
		DeployAccountTransactionV1: &pbstarknet.DeployAccountTransactionV1{
			MaxFee:              convertFelt(tx.MaxFee),
			Version:             string(tx.Version),
			Signature:           convertFeltArray(tx.Signature),
			Nonce:               convertFelt(tx.Nonce),
			ContractAddressSalt: convertFelt(tx.ContractAddressSalt),
			ConstructorCalldata: convertFeltArray(tx.ConstructorCalldata),
			ClassHash:           convertFelt(tx.ClassHash),
		},
	}

}

func convertDeployAccountTransactionV3(tx starknetRPC.DeployAccountTxnV3) *pbstarknet.TransactionWithReceipt_DeployAccountTransactionV3 {
	return &pbstarknet.TransactionWithReceipt_DeployAccountTransactionV3{
		DeployAccountTransactionV3: &pbstarknet.DeployAccountTransactionV3{
			Version:                   string(tx.Version),
			Signature:                 convertFeltArray(tx.Signature),
			Nonce:                     convertFelt(tx.Nonce),
			ContractAddressSalt:       convertFelt(tx.ContractAddressSalt),
			ClassHash:                 convertFelt(tx.ClassHash),
			ResourceBounds:            convertResourceBounds(tx.ResourceBounds),
			Tip:                       mustConvertTip(tx.Tip),
			PaymasterData:             convertFeltArray(tx.PayMasterData),
			NonceDataAvailabilityMode: convertFeeMode(tx.NonceDataMode),
			FeeDataAvailabilityMode:   convertFeeMode(tx.FeeMode),
		},
	}
}

func mustConvertU64(n starknetRPC.U64) uint64 {
	i, err := n.ToUint64()
	if err != nil {
		panic(fmt.Errorf("converting U64 to uint64: %w", err))
	}
	return i
}

func convertFeeMode(m starknetRPC.DataAvailabilityMode) pbstarknet.FEE_DATA_AVAILABILITY_MODE {
	switch m {
	case starknetRPC.DAModeL1:
		return pbstarknet.FEE_DATA_AVAILABILITY_MODE_L1
	case starknetRPC.DAModeL2:
		return pbstarknet.FEE_DATA_AVAILABILITY_MODE_L2
	default:
		panic(fmt.Errorf("unknown fee mode %v", m))
	}
}

func convertFeltArray(in []*felt.Felt) [][]byte {
	var out [][]byte
	for _, f := range in {
		out = append(out, convertFelt(f))
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
