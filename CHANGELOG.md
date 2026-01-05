# Change log

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), and this
project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html). See [MAINTAINERS.md](./MAINTAINERS.md)
for instructions to keep up to date.

## v1.1.2

This release will add a lot of delay between produced blocks and finalized blocks (the ones that get merged) to reflect the long "reversible segment" on Starknet.

* **BREAKING** Fix: eth-endpoints were not actually used to determine the last finalized block (LIB), they were always set to HEAD-1.
* **BREAKING** Add `default-lib-distance-to-head` (default=2000): This was previously always set to "1", meaning that block HEAD-1 was considered finalized. This has changed to reflect the actual LIB distance to the head block.

* Add `max-lib-distance-to-head` (default=0 -- disabled): Added a way to cap the LIB distance to the head block when eth-endpoints will show it as not progressing.

## v1.1.1

* Re-release of v1.1.0 to fix Docker CI action.

## v1.1.0

* Made `firestarknet rpc fetch --eth-endpoints` flag optional.

* Added building of Docker ARM64 images.

## v1.0.0

* First 1.0.0 version.

## v0.2.0

* Re-built using new pipeline, images now built with Ubuntu 24.04.

## v0.1.0

* First RPC poller version.
