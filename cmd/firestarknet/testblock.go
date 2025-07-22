package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	starknetRPC "github.com/NethermindEth/starknet.go/rpc"
	"github.com/go-json-experiment/json"
	"github.com/hexops/gotextdiff"
	"github.com/hexops/gotextdiff/myers"
	"github.com/hexops/gotextdiff/span"
	"github.com/itchyny/gojq"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/cli/sflags"
	ethRPC "github.com/streamingfast/eth-go/rpc"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	pbstarknet "github.com/streamingfast/firehose-starknet/pb/sf/starknet/type/v1"
	"github.com/streamingfast/firehose-starknet/rpc"
	"github.com/tidwall/gjson"
)

var TestBlockCommand = Command(testBlockE,
	"test-block <first-streamable-block> <starknet-rpc-endpoint>",
	"Test Starknet block fetcher",
	ExactArgs(2),
	Flags(func(flags *pflag.FlagSet) {
		flags.StringArray("eth-endpoints", []string{""}, "List of Ethereum clients to use to fetch the LIB")
		flags.String("fetch-lib-contract-address", "0xc662c410c0ecf747543f5ba90660f6abebd9c8c4", "The LIB contract address found on Ethereum. For Starknet-Testnet, pass in 0xe2bb56ee936fd6433dc0f6e7e3b8365c906aa057")
		flags.String("state-dir", "/data/poller", "interval between fetch")
		flags.Duration("interval-between-fetch", 0, "interval between fetch")
		flags.Duration("latest-block-retry-interval", time.Second, "interval between fetch")
		flags.Int("block-fetch-batch-size", 10, "Number of blocks to fetch in a single batch")
		flags.Duration("max-block-fetch-duration", 3*time.Second, "maximum delay before considering a block fetch as failed")
	}),
)

func testBlockE(cmd *cobra.Command, args []string) error {
	blockNum, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("parsing block number: %w", err)
	}
	starknetEndpoint := args[1]

	starknetClient, err := starknetRPC.NewProvider(starknetEndpoint)
	if err != nil {
		return fmt.Errorf("creating starknet client: %w", err)
	}

	ethEndpoints := sflags.MustGetStringArray(cmd, "eth-endpoints")

	var ethClients *firecoreRPC.Clients[*ethRPC.Client]
	if len(ethEndpoints) > 0 {
		ethClients = firecoreRPC.NewClients(1*time.Second, firecoreRPC.NewStickyRollingStrategy[*ethRPC.Client](), logger)
		for _, ethEndpoint := range ethEndpoints {
			ethClients.Add(ethRPC.NewClient(ethEndpoint))
		}
	}

	l1ContractAddress := "0xc662c410c0ecf747543f5ba90660f6abebd9c8c4"
	fetcher := rpc.NewFetcher(ethClients, l1ContractAddress, time.Duration(0), time.Duration(0), logger)

	// ---------------------------------------------------------
	// SF BLOCK
	// ---------------------------------------------------------

	sfBlockData, sfStateUpdateData, err := sfBlockData(cmd.Context(), starknetClient, fetcher, blockNum)
	if err != nil {
		return fmt.Errorf("fetching sf block: %w", err)
	}

	jqQueries := []string{
		`del(.state_update)`,
		`del(.transactions[].receipt.contract_address | (select(. == "")))`,
		`del(.transactions[].receipt.message_hash | (select(. == "")))`,
		`del(.transactions[].receipt.revert_reason | (select(. == "")))`,
		`del(.transactions[].receipt.contract_address | (select(. == "0x0")))`,
		`walk(if type == "object" then with_entries(select(.key != "max_fee" or .value != "0x0")) else . end)`,
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

	err = os.WriteFile("sf-block.json", sfBlockPretty, 0644)
	if err != nil {
		return fmt.Errorf("writing sf block: %w", err)
	}

	// ---------------------------------------------------------
	// OG BLOCK
	// ---------------------------------------------------------

	ogBlockData, err := rpcBlockData(cmd.Context(), starknetClient, blockNum)
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
		`walk(if type == "object" then with_entries(select(.key != "max_fee" or .value != "0x0")) else . end)`,
	}

	jqOgBlock, err := jqs(jqQueries, ogBlockData)
	if err != nil {
		return fmt.Errorf("jqing og block: %w", err)
	}

	ogBlockPretty, err := pretty(jqOgBlock)
	if err != nil {
		return fmt.Errorf("prettying og block: %w", err)
	}

	err = os.WriteFile("og-block.json", ogBlockPretty, 0644)
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

	err = os.WriteFile("sf-state.json", sfStateUpdatePretty, 0644)
	if err != nil {
		return fmt.Errorf("writing sf state: %w", err)
	}

	// ---------------------------------------------------------
	// OG State Update
	// ---------------------------------------------------------
	ogStateUpdate, err := rpcStateUpdateData(cmd.Context(), starknetClient, blockNum)
	if err != nil {
		return fmt.Errorf("fetching state update: %w", err)
	}

	jqOgStateUpdate, err := jqs([]string{
		`del(.block_hash)`,
		`(.. | select(type == "null")) |= ""`,
		`del(.. | select(length == 0))`,
	}, ogStateUpdate)
	if err != nil {
		return fmt.Errorf("jqing og state: %w", err)
	}

	ogStateUpdatePretty, err := pretty(jqOgStateUpdate)
	if err != nil {
		return fmt.Errorf("prettying og state: %w", err)
	}
	err = os.WriteFile("og-state.json", ogStateUpdatePretty, 0644)
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

func sfBlockData(ctx context.Context, starknetClient *starknetRPC.Provider, fetcher *rpc.Fetcher, blockNum uint64) ([]byte, []byte, error) {
	bstreamBlock, _, err := fetcher.Fetch(ctx, starknetClient, blockNum)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching block: %w", err)
	}

	block := &pbstarknet.Block{}
	err = bstreamBlock.Payload.UnmarshalTo(block)
	if err != nil {
		return nil, nil, fmt.Errorf("unmarshalling block: %w", err)
	}

	blockData, err := json.Marshal(block, jsonOptions...)
	if err != nil {
		return nil, nil, fmt.Errorf("marshalling block: %w", err)
	}

	stateUpdateData, err := json.Marshal(block.StateUpdate, jsonOptions...)
	if err != nil {
		return nil, nil, fmt.Errorf("marshalling state update: %w", err)
	}

	return blockData, stateUpdateData, nil
}

func rpcBlockData(ctx context.Context, starknetClient *starknetRPC.Provider, blockNum uint64) ([]byte, error) {
	block, err := rpc.FetchBlock(ctx, starknetClient, blockNum)
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

func rpcStateUpdateData(ctx context.Context, starknetClient *starknetRPC.Provider, blockNum uint64) ([]byte, error) {
	stateUpdate, err := rpc.FetchStateUpdate(ctx, starknetClient, blockNum)
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
