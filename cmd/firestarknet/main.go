package main

import (
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/logging"
	"go.uber.org/zap"
)

// Injected at build time
var version = ""

var logger, _ = logging.PackageLogger("firestarknet", "github.com/streamingfast/firehose-starknet")

func main() {
	logging.InstantiateLoggers(logging.WithDefaultLevel(zap.InfoLevel))

	Run(
		"firestarknet",
		"Firehose Starknet block fetching and tooling",

		ConfigureVersion(version),
		ConfigureViper("FIRESTARKNET"),

		FetchCommand,
		TestBlockCommand,

		OnCommandErrorLogAndExit(logger),
	)
}
