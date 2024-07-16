<a href="https://www.streamingfast.io/">
    <img width="100%" src="https://github.com/streamingfast/firehose-ethereum/raw/develop/assets/firehose-banner.png" alt="StreamingFast Fireshose Banner" />
</a>

# Firehose for Starknet

[![reference](https://img.shields.io/badge/godoc-reference-5272B4.svg?style=flat-square)](https://pkg.go.dev/github.com/streamingfast/firehose-solana)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

Quickstart with Firehose for Starknet can be found in the official Firehose docs. Here some quick links to it:

- [Firehose Overview](https://firehose.streamingfast.io/introduction/firehose-overview)
- [Concepts & Architectures](https://firehose.streamingfast.io/concepts-and-architeceture)
  - [Components](https://firehose.streamingfast.io/concepts-and-architeceture/components)
  - [Data Flow](https://firehose.streamingfast.io/concepts-and-architeceture/data-flow)
  - [Data Storage](https://firehose.streamingfast.io/concepts-and-architeceture/data-storage)
  - [Design Principles](https://firehose.streamingfast.io/concepts-and-architeceture/design-principles)

# Running the Firehose poller

The below command with start streaming Firehose Starknet blocks, check `proto/sf/starknet/type/v1/block.proto` for more information.

```bash
firestarknet fetch $FIRST_STREAMABLE_BLOCK --state-dir $STATE_DIR --block-fetch-batch-size=1 --interval-between-fetch=0s --latest-block-retry-interval=10s --starknet-endpoints $STARKNET_RPC_ENDPOINT --eth-endpoints $ETH_ENDPOINT
# FIRST_STREAMABLE_BLOCK: this would often be set to 0
# STATE_DIR: Location to store the Firehose Starknet blocks
# STARKNET_RPC_ENDPOINT: RPC Endpoint for Starknet to make calls to fetch blocks with receitps, state udpdate, block number and latest block number
# ETH_ENDPOINT: L1 ETH RPC Endpoint to fetch the L1 accept block
```

## Contributing

Report any protocol-specific issues in their
[respective repositories](https://github.com/streamingfast/streamingfast#protocols)

**Please first refer to the general
[StreamingFast contribution guide](https://github.com/streamingfast/streamingfast/blob/master/CONTRIBUTING.md)**,
if you wish to contribute to this code base.

### License

[Apache 2.0](LICENSE)
