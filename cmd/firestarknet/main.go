package main

import (
	. "github.com/streamingfast/cli"
	"github.com/streamingfast/logging"
	"go.uber.org/zap"
)

// Injected at build time
var version = "<missing>"

var logger, _ = logging.PackageLogger("firestarknet", "github.com/streamingfast/firehose-starknet")

func main() {
	logging.InstantiateLoggers(logging.WithDefaultLevel(zap.InfoLevel))

	Run(
		"firestarknet",
		"Firehose Starknet block fetching and tooling",
		Description(`
			Firehose Starknet implements the Firehose Reader protocol for Starknet,
			via 'firestarknet rpc fetch <flags>' (see 'firestarknet rpc fetch --help').

			It is expected to be used with the Firehose Stack by operating 'firecore'
			binary which spans Firehose Starknet Reader as a subprocess and reads from
			it producing blocks and offering Firehose & Substreams APIs.

			Read the Firehose documentation at firehose.streamingfast.io for more
			information how to use this binary.

			The binary also contains a few commands to test the Starknet block
			fetching capabilities, such as fetching a block by number or hash.
		`),

		ConfigureVersion(version),
		ConfigureViper("FIRESTARKNET"),

		FetchCommand,
		TestBlockCommand,

		OnCommandErrorLogAndExit(logger),
	)
}
