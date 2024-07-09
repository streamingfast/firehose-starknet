#!/bin/bash
for block in `seq 1 10000 650000`
do
  go run ./cmd/firestarknet test-block $block https://dry-purple-firefly.strk-mainnet.quiknode.pro/6f9c63fd4b54d5d17cc8cd53e5f1cad3a1dce577/  https://radial-long-grass.quiknode.pro/ea18b0cd3378cfc1e67752010196f344939b6187/
  if [ $? -ne 0 ]; then
    echo "Error at block $block"
    exit 1
  fi
done
echo "Goodbye!"