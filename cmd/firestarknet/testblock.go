package main

import (
	"context"
	pbstarknet "firehose-starknet/pb/sf/starknet/type/v1"
	"firehose-starknet/rpc"
	"fmt"
	"io/ioutil"
	"strconv"
	"time"

	starknetRPC "github.com/NethermindEth/starknet.go/rpc"
	"github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
	"github.com/itchyny/gojq"
	"github.com/spf13/cobra"
	ethRPC "github.com/streamingfast/eth-go/rpc"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	"github.com/streamingfast/logging"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
)

func NewTestBlockCmd(logger *zap.Logger, tracer logging.Tracer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test-block",
		Short: "Test block",
		Args:  cobra.ExactArgs(3),

		RunE: func(cmd *cobra.Command, args []string) error {
			return testBlock(cmd.Context(), logger, tracer, args)
		},
	}

	return cmd
}

var protoOptions = protojson.MarshalOptions{
	Multiline:         true,
	Indent:            "  ",
	AllowPartial:      false,
	UseProtoNames:     true,
	UseEnumNumbers:    false,
	EmitUnpopulated:   false,
	EmitDefaultValues: true,
	Resolver:          nil,
}

var marshalers = json.NewMarshalers(
	json.MarshalFuncV2(encodeTransactionWithReceipt),
)

var jsonOptions = []json.Options{
	json.StringifyNumbers(true),
	json.RejectUnknownMembers(false),
}

func init() {
	jsonOptions = append(jsonOptions, json.WithMarshalers(marshalers))
}

func testBlock(ctx context.Context, logger *zap.Logger, tracer logging.Tracer, args []string) error {

	blockNum, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("parsing block number: %w", err)
	}
	starknetEndpoint := args[1]
	ethEndpoint := args[2]

	starknetClient, err := starknetRPC.NewProvider(starknetEndpoint)
	if err != nil {
		return fmt.Errorf("creating starknet client: %w", err)
	}
	starknetClients := firecoreRPC.NewClients[*starknetRPC.Provider]()
	starknetClients.Add(starknetClient)

	ethClients := firecoreRPC.NewClients[*ethRPC.Client]()
	ethClients.Add(ethRPC.NewClient(ethEndpoint))

	l1ContractAddress := "0xc662c410c0ecf747543f5ba90660f6abebd9c8c4"
	fetcher := rpc.NewFetcher(starknetClients, ethClients, l1ContractAddress, time.Duration(0), time.Duration(0), logger)

	// ---------------------------------------------------------
	// SF BLOCK
	// ---------------------------------------------------------

	sfBlockData, sfStateUpdateData, err := sfBlockData(ctx, fetcher, blockNum)
	if err != nil {
		return fmt.Errorf("fetching sf block: %w", err)
	}

	jqQueries := []string{
		`del(.state_update)`,
		`del(.transactions[].receipt.contract_address | (select(. == "")))`,
		`del(.transactions[].receipt.message_hash | (select(. == "")))`,
		`del(.transactions[].receipt.revert_reason | (select(. == "")))`,
	}

	sfJqBlock, err := jqs(jqQueries, sfBlockData)
	if err != nil {
		return fmt.Errorf("jqing sf block: %w", err)
	}

	sfBlockPretty, err := pretty(sfJqBlock)
	if err != nil {
		return fmt.Errorf("prettying sf block: %w", err)
	}

	err = ioutil.WriteFile("sf-block.json", sfBlockPretty, 0644)
	if err != nil {
		return fmt.Errorf("writing sf block: %w", err)
	}

	// ---------------------------------------------------------
	// OG BLOCK
	// ---------------------------------------------------------

	ogBlockData, err := rpcBlockData(ctx, starknetClients, blockNum)
	if err != nil {
		return fmt.Errorf("fetching rpc block: %w", err)
	}
	jqOgBlock, err := jq(`
		del(
			.status,
			.transactions[].invoke_transaction_v0.type,
			.transactions[].invoke_transaction_v1.type,
			.transactions[].invoke_transaction_v3.type,
			.transactions[].l1_handler_transaction.type,
			.transactions[].declare_transaction_v0.type,
			.transactions[].declare_transaction_v1.type,
			.transactions[].declare_transaction_v2.type,
			.transactions[].declare_transaction_v3.type,
			.transactions[].deploy_transaction_v0.type,
			.transactions[].deploy_account_transaction_v1.type,
			.transactions[].deploy_account_transaction_v3.type,
            .transactions[].receipt.finality_status
			)`, ogBlockData)
	if err != nil {
		return fmt.Errorf("jqing og block: %w", err)
	}

	ogBlockPretty, err := pretty(jqOgBlock)
	if err != nil {
		return fmt.Errorf("prettying og block: %w", err)
	}

	err = ioutil.WriteFile("og-block.json", ogBlockPretty, 0644)
	if err != nil {
		return fmt.Errorf("writing og block: %w", err)
	}

	fmt.Println("---------------------------------------")
	fmt.Println("Block Diffs")
	fmt.Println("---------------------------------------")
	edits := myers.ComputeEdits(span.URIFromPath("block"), string(sfBlockPretty), string(ogBlockPretty))
	diff := fmt.Sprint(gotextdiff.ToUnified("sf", "og", string(sfBlockPretty), edits))
	fmt.Println(diff)

	// ---------------------------------------------------------
	// SF State Update
	// ---------------------------------------------------------
	sfStateUpdatePretty, err := pretty(sfStateUpdateData)
	if err != nil {
		return fmt.Errorf("prettying sf block: %w", err)
	}
	err = ioutil.WriteFile("sf-state.json", sfStateUpdatePretty, 0644)
	if err != nil {
		return fmt.Errorf("writing sf state: %w", err)
	}

	// ---------------------------------------------------------
	// OG State Update
	// ---------------------------------------------------------
	ogStateUpdate, err := rpcStateUpdateData(ctx, starknetClients, blockNum)
	if err != nil {
		return fmt.Errorf("fetching state update: %w", err)
	}

	jqOgStateUpdate, err := jqs([]string{`del(.block_hash)`}, ogStateUpdate)

	ogStateUpdatePretty, err := pretty(jqOgStateUpdate)
	if err != nil {
		return fmt.Errorf("prettying sf block: %w", err)
	}
	err = ioutil.WriteFile("og-state.json", ogStateUpdatePretty, 0644)
	if err != nil {
		return fmt.Errorf("writing og state: %w", err)
	}

	fmt.Println("---------------------------------------")
	fmt.Println("State Update")
	fmt.Println("---------------------------------------")
	edits = myers.ComputeEdits(span.URIFromPath("block"), string(sfStateUpdatePretty), string(ogStateUpdatePretty))
	diff = fmt.Sprint(gotextdiff.ToUnified("sf", "og", string(sfStateUpdatePretty), edits))
	fmt.Println(diff)

	return nil
}

func sfBlockData(ctx context.Context, fetcher *rpc.Fetcher, blockNum uint64) ([]byte, []byte, error) {
	bstreamBlock, _, err := fetcher.Fetch(ctx, blockNum)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching block: %w", err)
	}

	block := &pbstarknet.Block{}
	bstreamBlock.Payload.UnmarshalTo(block)

	blockData, err := protoOptions.Marshal(block)
	if err != nil {
		return nil, nil, fmt.Errorf("marshalling block: %w", err)
	}

	stateUpdateData, err := protoOptions.Marshal(block.StateUpdate)

	return blockData, stateUpdateData, nil
}

func rpcBlockData(ctx context.Context, starknetClients *firecoreRPC.Clients[*starknetRPC.Provider], blockNum uint64) ([]byte, error) {
	block, err := rpc.FetchBlock(ctx, starknetClients, blockNum)
	if err != nil {
		return nil, fmt.Errorf("fetching block: %w", err)
	}
	block.BlockStatus = ""

	data, err := json.Marshal(
		block,
		jsonOptions...,
	)
	if err != nil {
		return nil, fmt.Errorf("marshalling block: %w", err)
	}

	return data, nil
}

func rpcStateUpdateData(ctx context.Context, starknetClients *firecoreRPC.Clients[*starknetRPC.Provider], blockNum uint64) ([]byte, error) {
	stateUpdate, err := rpc.FetchStateUpdate(ctx, starknetClients, blockNum)
	if err != nil {
		return nil, fmt.Errorf("fetching block: %w", err)
	}

	data, err := json.Marshal(
		stateUpdate,
		jsonOptions...,
	)

	if err != nil {
		return nil, fmt.Errorf("marshalling stateUpdate: %w", err)
	}

	return data, nil
}

func jqs(queries []string, data []byte) ([]byte, error) {
	for _, query := range queries {
		var err error
		data, err = jq(query, data)
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func jq(query string, data []byte) ([]byte, error) {

	a := map[string]interface{}{}
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("unmarshalling data: %w", err)
	}

	q, err := gojq.Parse(query)
	if err != nil {
		return nil, fmt.Errorf("parsing jq query: %w", err)
	}

	iter := q.Run(a)
	v, _ := iter.Next()
	if err, ok := v.(error); ok {
		return nil, fmt.Errorf("running jq query: %w", err)
	}

	b, err := json.Marshal(v, jsonOptions...)
	if err != nil {
		return nil, fmt.Errorf("marshalling oject: %w", err)
	}

	return b, nil
}

func pretty(b []byte) ([]byte, error) {
	r := gjson.ParseBytes(b)
	r = r.Get(`@pretty:{"sortKeys":true} `)
	return []byte(r.Raw), nil
}

func encodeTransactionWithReceipt(encoder *jsontext.Encoder, transactionWithReceipt starknetRPC.TransactionWithReceipt, options json.Options) error {

	tx := transactionWithReceipt.Transaction.Transaction

	name := mustTransactionName(tx)
	cnt, err := json.Marshal(
		tx,
		jsonOptions...,
	)

	if err != nil {
		return fmt.Errorf("json marshalling tx: %w", err)
	}

	if err := encoder.WriteToken(jsontext.ObjectStart); err != nil {
		return fmt.Errorf("writing ObjectStart token: %w", err)
	}

	if err := encoder.WriteToken(jsontext.String(name)); err != nil {
		return fmt.Errorf("writing token: %w", err)
	}

	if err := encoder.WriteValue(cnt); err != nil {
		return fmt.Errorf("writing tx value: %w", err)
	}

	if err := encoder.WriteToken(jsontext.String("receipt")); err != nil {
		return fmt.Errorf("writing token: %w", err)
	}

	receipt := transactionWithReceipt.Receipt.TransactionReceipt
	cnt, err = json.Marshal(
		receipt,
		jsonOptions...,
	)
	if err != nil {
		return fmt.Errorf("json marshalling receipt: %w", err)
	}

	if err := encoder.WriteValue(cnt); err != nil {
		return fmt.Errorf("writing receipt value: %w", err)
	}

	if err := encoder.WriteToken(jsontext.ObjectEnd); err != nil {
		return fmt.Errorf("writing ObjectEnd token: %w", err)
	}

	return nil
}

func mustTransactionName(tx starknetRPC.Transaction) string {
	switch t := tx.(type) {
	case starknetRPC.InvokeTxnV0:
		t.Type = ""
		return "invoke_transaction_v0"
	case starknetRPC.InvokeTxnV1:
		t.Type = ""
		return "invoke_transaction_v1"
	case starknetRPC.InvokeTxnV3:
		t.Type = ""
		return "invoke_transaction_v3"
	case starknetRPC.L1HandlerTxn:
		t.Type = ""
		return "l1_handler_transaction"
	case starknetRPC.DeclareTxnV0:
		t.Type = ""
		return "declare_transaction_v0"
	case starknetRPC.DeclareTxnV1:
		t.Type = ""
		return "declare_transaction_v1"
	case starknetRPC.DeclareTxnV2:
		t.Type = ""
		return "declare_transaction_v2"
	case starknetRPC.DeclareTxnV3:
		t.Type = ""
		return "declare_transaction_v3"
	case starknetRPC.DeployTxn:
		t.Type = ""
		return "deploy_transaction_v0"
	case starknetRPC.DeployAccountTxn:
		t.Type = ""
		return "deploy_account_transaction_v1"
	case starknetRPC.DeployAccountTxnV3:
		t.Type = ""
		return "deploy_account_transaction_v3"
	default:
		panic(fmt.Errorf("unknown transaction type %T", tx))
	}
}
