package main

import (
	"firehose-starknet/rpc"
	"fmt"
	"strconv"
	"time"

	snRPC "github.com/NethermindEth/starknet.go/rpc"
	"github.com/spf13/cobra"
	"github.com/streamingfast/cli/sflags"
	goRPC "github.com/streamingfast/eth-go/rpc"
	firecore "github.com/streamingfast/firehose-core"
	"github.com/streamingfast/firehose-core/blockpoller"
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
	cmd.Flags().String("fetch-lib-contract-address", "0xc662c410c0ecf747543f5ba90660f6abebd9c8c4", "The LIB contract address found on Ethereum")
	cmd.Flags().String("state-dir", "/data/poller", "interval between fetch")
	cmd.Flags().Duration("interval-between-fetch", 0, "interval between fetch")
	cmd.Flags().Duration("latest-block-retry-interval", time.Second, "interval between fetch")
	cmd.Flags().Int("block-fetch-batch-size", 10, "Number of blocks to fetch in a single batch")

	return cmd
}

func fetchRunE(logger *zap.Logger, tracer logging.Tracer) firecore.CommandExecutor {
	return func(cmd *cobra.Command, args []string) (err error) {
		ctx := cmd.Context()
		rpcEndpoints := sflags.MustGetStringArray(cmd, "starknet-endpoints")
		ethEndpoints := sflags.MustGetStringArray(cmd, "eth-endpoints")
		fetchLIBContractAddress := sflags.MustGetString(cmd, "fetch-lib-contract-address")
		stateDir := sflags.MustGetString(cmd, "state-dir")
		startBlock, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("unable to parse first streamable block %d: %w", startBlock, err)
		}

		fetchInterval := sflags.MustGetDuration(cmd, "interval-between-fetch")

		logger.Info(
			"launching firehose-starknet poller",
			zap.Strings("rpc_endpoint", rpcEndpoints),
			zap.String("state_dir", stateDir),
			zap.Uint64("first_streamable_block", startBlock),
			zap.Duration("interval_between_fetch", fetchInterval),
			zap.Duration("latest_block_retry_interval", sflags.MustGetDuration(cmd, "latest-block-retry-interval")),
		)

		latestBlockRetryInterval := sflags.MustGetDuration(cmd, "latest-block-retry-interval")

		rpcClients := make([]*snRPC.Provider, 0, len(rpcEndpoints))
		for _, rpcEndpoint := range rpcEndpoints {
			client, err := snRPC.NewProvider(rpcEndpoint)
			if err != nil {
				return fmt.Errorf("creating rpc client: %w", err)
			}
			rpcClients = append(rpcClients, client)
		}

		ethRpcEndpoints := make([]*goRPC.Client, 0, len(ethEndpoints))
		for _, ethEndpoint := range ethEndpoints {
			client := goRPC.NewClient(ethEndpoint)
			ethRpcEndpoints = append(ethRpcEndpoints, client)
		}
		starkClients := rpc.NewRPCClient(ethRpcEndpoints)
		rpcFetcher := rpc.NewFetcher(rpcClients, starkClients, fetchLIBContractAddress, fetchInterval, latestBlockRetryInterval, logger)

		poller := blockpoller.New(
			rpcFetcher,
			blockpoller.NewFireBlockHandler("type.googleapis.com/sf.starknet.type.v1.Block"),
			blockpoller.WithStoringState(stateDir),
			blockpoller.WithLogger(logger),
		)

		err = poller.Run(ctx, startBlock, sflags.MustGetInt(cmd, "block-fetch-batch-size"))
		if err != nil {
			return fmt.Errorf("running poller: %w", err)
		}

		return nil
	}
}
