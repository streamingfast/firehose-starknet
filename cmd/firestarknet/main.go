package main

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/streamingfast/firehose-core/cmd/tools"
	"github.com/streamingfast/logging"
	"go.uber.org/zap"
	"os"
)

var logger, tracer = logging.PackageLogger("firestarknet", "github.com/streamingfast/firehose-starknet")
var rootCmd = &cobra.Command{
	Use:   "firestarknet",
	Short: "firestarknet block fetching and tooling",
	Args:  cobra.ExactArgs(1),
}

func init() {
	logging.InstantiateLoggers(logging.WithDefaultLevel(zap.InfoLevel))

	rootCmd.AddCommand(NewFetchCmd(logger, tracer))
	rootCmd.AddCommand(tools.ToolsCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Whoops. There was an error while executing your CLI '%s'", err)
		os.Exit(1)
	}
}
