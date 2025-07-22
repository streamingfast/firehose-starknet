package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/streamingfast/firehose-starknet/rpc"

	starknetRPC "github.com/NethermindEth/starknet.go/rpc"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/cli/sflags"
	ethRPC "github.com/streamingfast/eth-go/rpc"
	"github.com/streamingfast/firehose-core/blockpoller"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	"go.uber.org/zap"
)

var FetchCommand = Command(fetchE,
	"fetch <first-streamable-block>",
	"Fetch blocks from RPC endpoint and produce Firehose blocks for consumption by Firehose Core",
	ExactArgs(1),
	Flags(func(flags *pflag.FlagSet) {
		flags.StringArray("starknet-endpoints", []string{""}, "List of endpoints to use to fetch different method calls")
		flags.StringArray("eth-endpoints", nil, "List of Ethereum clients to use to fetch the LIB (optional)")
		flags.String("fetch-lib-contract-address", "0xc662c410c0ecf747543f5ba90660f6abebd9c8c4", "The LIB contract address found on Ethereum. For Starknet-Testnet, pass in 0xe2bb56ee936fd6433dc0f6e7e3b8365c906aa057")
		flags.String("state-dir", "/data/poller", "interval between fetch")
		flags.Duration("interval-between-fetch", 0, "interval between fetch")
		flags.Duration("latest-block-retry-interval", time.Second, "interval between fetch")
		flags.Int("block-fetch-batch-size", 10, "Number of blocks to fetch in a single batch")
		flags.Duration("max-block-fetch-duration", 3*time.Second, "maximum delay before considering a block fetch as failed")
	}),
)

func fetchE(cmd *cobra.Command, args []string) (err error) {
	startBlock, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("unable to parse first streamable block %d: %w", startBlock, err)
	}
	rpcEndpoints := sflags.MustGetStringArray(cmd, "starknet-endpoints")
	ethEndpoints := sflags.MustGetStringArray(cmd, "eth-endpoints")

	fetchLIBContractAddress := sflags.MustGetString(cmd, "fetch-lib-contract-address")
	stateDir := sflags.MustGetString(cmd, "state-dir")

	fetchInterval := sflags.MustGetDuration(cmd, "interval-between-fetch")
	maxBlockFetchDuration := sflags.MustGetDuration(cmd, "max-block-fetch-duration")

	logger.Info(
		"launching firehose-starknet poller",
		zap.Strings("rpc_endpoint", rpcEndpoints),
		zap.String("state_dir", stateDir),
		zap.Uint64("first_streamable_block", startBlock),
		zap.Duration("interval_between_fetch", fetchInterval),
		zap.Duration("latest_block_retry_interval", sflags.MustGetDuration(cmd, "latest-block-retry-interval")),
	)

	latestBlockRetryInterval := sflags.MustGetDuration(cmd, "latest-block-retry-interval")
	rollingStrategy := firecoreRPC.NewStickyRollingStrategy[*starknetRPC.Provider]()

	starknetClients := firecoreRPC.NewClients(maxBlockFetchDuration, rollingStrategy, logger)
	for _, rpcEndpoint := range rpcEndpoints {
		client, err := starknetRPC.NewProvider(rpcEndpoint)
		if err != nil {
			return fmt.Errorf("creating rpc client: %w", err)
		}
		starknetClients.Add(client)
	}

	var ethClients *firecoreRPC.Clients[*ethRPC.Client]
	if len(ethEndpoints) > 0 {
		ethRollingStrategy := firecoreRPC.NewStickyRollingStrategy[*ethRPC.Client]()
		ethClients = firecoreRPC.NewClients(maxBlockFetchDuration, ethRollingStrategy, logger)
		for _, ethEndpoint := range ethEndpoints {
			ethClients.Add(ethRPC.NewClient(ethEndpoint))
		}
	}

	rpcFetcher := rpc.NewFetcher(ethClients, fetchLIBContractAddress, fetchInterval, latestBlockRetryInterval, logger)

	poller := blockpoller.New(
		rpcFetcher,
		blockpoller.NewFireBlockHandler("type.googleapis.com/sf.starknet.type.v1.Block"),
		starknetClients,
		blockpoller.WithStoringState[*starknetRPC.Provider](stateDir),
		blockpoller.WithLogger[*starknetRPC.Provider](logger),
	)

	err = poller.Run(startBlock, nil, sflags.MustGetInt(cmd, "block-fetch-batch-size"))
	if err != nil {
		return fmt.Errorf("running poller: %w", err)
	}

	return nil
}
