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
		`del(.transactions[].receipt.contract_address | (select(. == "0x0")))`,
	}

	sfJqBlock, err := jqs(jqQueries, sfBlockData)
	if err != nil {
		fmt.Println(string(sfBlockData))
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

	jqQueries = []string{
		`del(
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
			)`,
		`(.. | select(type == "null")) |= ""`,
		`del(.. | select(length == 0))`,
	}

	jqOgBlock, err := jqs(jqQueries, ogBlockData)
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
	if len(edits) > 0 {
		return fmt.Errorf("block has diffs")
	}

	// ---------------------------------------------------------
	// SF State Update
	// ---------------------------------------------------------
	jqQueries = []string{
		`del(.state_diff.replaced_classes[]?.contract_address? | (select(. == "0x0")))`,
		`del(.. | select(length == 0))`,
	}

	jqSfStateUpdate, err := jqs(jqQueries, sfStateUpdateData)
	if err != nil {
		return fmt.Errorf("jqing sf state: %w", err)
	}

	sfStateUpdatePretty, err := pretty(jqSfStateUpdate)
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

	jqOgStateUpdate, err := jqs([]string{
		`del(.block_hash)`,
		`(.. | select(type == "null")) |= ""`,
		`del(.. | select(length == 0))`,
	}, ogStateUpdate)

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

	if len(edits) > 0 {
		return fmt.Errorf("state update has diffs")
	}

	return nil
}

func sfBlockData(ctx context.Context, fetcher *rpc.Fetcher, blockNum uint64) ([]byte, []byte, error) {
	bstreamBlock, _, err := fetcher.Fetch(ctx, blockNum)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching block: %w", err)
	}

	block := &pbstarknet.Block{}
	err = bstreamBlock.Payload.UnmarshalTo(block)
	if err != nil {
		return nil, nil, fmt.Errorf("unmarshalling block: %w", err)
	}

	blockData, err := json.Marshal(block, jsonOptions...)

	stateUpdateData, err := json.Marshal(block.StateUpdate, jsonOptions...)
	if err != nil {
		return nil, nil, fmt.Errorf("marshalling state update: %w", err)
	}

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
