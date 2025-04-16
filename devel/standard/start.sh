#!/usr/bin/env bash

ROOT="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

clean=

main() {
  check_firecore

  pushd "$ROOT" &> /dev/null

  while getopts "hc" opt; do
    case $opt in
      h) usage && exit 0;;
      c) clean=true;;
      \?) usage_error "Invalid option: -$OPTARG";;
    esac
  done
  shift $((OPTIND-1))
  [[ $1 = "--" ]] && shift

  fh_data_dir="$ROOT/firehose-data"

  set -e

  if [[ $clean == "true" ]]; then
    rm -rf "$fh_data_dir" &> /dev/null || true
  fi

  firecore -c $(basename $ROOT).yaml start "$@"
}

usage_error() {
  message="$1"
  exit_code="$2"

  echo "ERROR: $message"
  echo ""
  usage
  exit ${exit_code:-1}
}

check_firecore() {
  if ! command -v "firecore" &> /dev/null; then
    echo "The 'firecore' binary could not be found, you can install it through one of those means:"
    echo ""
    echo "- By running 'brew install streamingfast/tap/firehose-core' on Mac or Linux system (with Homebrew installed)"
    echo "- By building it from source cloning https://github.com/streamingfast/firehose-core.git and then 'go install ./cmd/fireeth'"
    echo "- By downloading a pre-compiled binary from https://github.com/streamingfast/firehose-core/releases"
    exit 1
  fi
}

usage() {
  echo "usage: start.sh [-c]"
  echo ""
  echo "Start $(basename $ROOT) environment."
  echo ""
  echo "Options"
  echo "    -c             Clean actual data directory first"
  echo ""
  echo "Examples"
  echo "   Stream blocks from HEAD      firecore tools firehose-client -p localhost:8089 -- -1 | jq ."
  echo "   Fetch specific block         firecore tools firehose-single-block-client -p localhost:8089 1321000 | jq ."
}

main "$@"
