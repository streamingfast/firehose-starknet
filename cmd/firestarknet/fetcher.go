package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/streamingfast/firehose-starknet/rpc"

	starknetRPC "github.com/NethermindEth/starknet.go/rpc"
	"github.com/spf13/cobra"
	"github.com/streamingfast/cli/sflags"
	ethRPC "github.com/streamingfast/eth-go/rpc"
	firecore "github.com/streamingfast/firehose-core"
	"github.com/streamingfast/firehose-core/blockpoller"
	firecoreRPC "github.com/streamingfast/firehose-core/rpc"
	"github.com/streamingfast/logging"
	"go.uber.org/zap"
)

func NewFetchCmd(logger *zap.Logger, tracer logging.Tracer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "fetch <first-streamable-block>",
		Short: "fetch blocks from rpc endpoint",
		Args:  cobra.ExactArgs(1),
		RunE:  fetchRunE(logger, tracer),
	}

	cmd.Flags().StringArray("starknet-endpoints", []string{""}, "List of endpoints to use to fetch different method calls")
	cmd.Flags().StringArray("eth-endpoints", []string{""}, "List of Ethereum clients to use to fetch the LIB")
	cmd.Flags().String("fetch-lib-contract-address", "0xc662c410c0ecf747543f5ba90660f6abebd9c8c4", "The LIB contract address found on Ethereum. For Starknet-Testnet, pass in 0xe2bb56ee936fd6433dc0f6e7e3b8365c906aa057")
	cmd.Flags().String("state-dir", "/data/poller", "interval between fetch")
	cmd.Flags().Duration("interval-between-fetch", 0, "interval between fetch")
	cmd.Flags().Duration("latest-block-retry-interval", time.Second, "interval between fetch")
	cmd.Flags().Int("block-fetch-batch-size", 10, "Number of blocks to fetch in a single batch")
	cmd.Flags().Duration("max-block-fetch-duration", 3*time.Second, "maximum delay before considering a block fetch as failed")

	return cmd
}

func fetchRunE(logger *zap.Logger, tracer logging.Tracer) firecore.CommandExecutor {
	return func(cmd *cobra.Command, args []string) (err error) {
		rpcEndpoints := sflags.MustGetStringArray(cmd, "starknet-endpoints")
		ethEndpoints := sflags.MustGetStringArray(cmd, "eth-endpoints")
		fetchLIBContractAddress := sflags.MustGetString(cmd, "fetch-lib-contract-address")
		stateDir := sflags.MustGetString(cmd, "state-dir")
		startBlock, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("unable to parse first streamable block %d: %w", startBlock, err)
		}

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

		starknetClients := firecoreRPC.NewClients[*starknetRPC.Provider](maxBlockFetchDuration, rollingStrategy, logger)
		for _, rpcEndpoint := range rpcEndpoints {
			client, err := starknetRPC.NewProvider(rpcEndpoint)
			if err != nil {
				return fmt.Errorf("creating rpc client: %w", err)
			}
			starknetClients.Add(client)
		}

		ethRollingStrategy := firecoreRPC.NewStickyRollingStrategy[*ethRPC.Client]()
		ethClients := firecoreRPC.NewClients[*ethRPC.Client](maxBlockFetchDuration, ethRollingStrategy, logger)
		for _, ethEndpoint := range ethEndpoints {
			ethClients.Add(ethRPC.NewClient(ethEndpoint))
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
}
