package rpc

import (
	"testing"

	"github.com/NethermindEth/juno/core/felt"
	"github.com/test-go/testify/require"

	"github.com/ethereum/go-ethereum/common/hexutil"
)

func Test_LIBNumConvertion(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected uint64
	}{
		{
			name:     "Test 1",
			input:    "0x000000000000000000000000000000000000000000000000000000000009f511",
			expected: 652561,
		},
		{
			name:     "Test 1",
			input:    "0x9f511",
			expected: 652561,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.input = cleanBlockNum(tt.input)
			result, err := hexutil.DecodeUint64(tt.input)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if result != tt.expected {
				t.Errorf("unexpected result: %v", result)
			}
		})
	}
}

func TestCalculateLIBNum(t *testing.T) {
	tests := []struct {
		name                     string
		blockNumber              uint64
		lastL1AcceptedBlock      *uint64
		defaultLIBDistanceToHead uint64
		maxLIBDistanceToHead     uint64
		expected                 uint64
		description              string
	}{
		// Basic cases with L1 accepted block
		{
			name:                     "L1 accepted block provided - normal case",
			blockNumber:              1000,
			lastL1AcceptedBlock:      uint64Ptr(500),
			defaultLIBDistanceToHead: 0,
			maxLIBDistanceToHead:     0,
			expected:                 500,
			description:              "Should use L1 accepted block when provided",
		},
		{
			name:                     "L1 accepted block provided - within max LIB",
			blockNumber:              1000,
			lastL1AcceptedBlock:      uint64Ptr(500),
			defaultLIBDistanceToHead: 123,
			maxLIBDistanceToHead:     600,
			expected:                 500,
			description:              "Should use L1 accepted block when provided within max LIB",
		},
		{
			name:                     "L1 accepted block newer than current - underflow protection",
			blockNumber:              100,
			lastL1AcceptedBlock:      uint64Ptr(200),
			defaultLIBDistanceToHead: 50,
			maxLIBDistanceToHead:     100,
			expected:                 99,
			description:              "Should cap to blockNumber-1 when L1 block is newer",
		},
		{
			name:                     "L1 accepted block newer than current - block 0 edge case",
			blockNumber:              0,
			lastL1AcceptedBlock:      uint64Ptr(100),
			defaultLIBDistanceToHead: 50,
			maxLIBDistanceToHead:     100,
			expected:                 0,
			description:              "Should handle block 0 without underflow",
		},
		{
			name:                     "L1 accepted block with max distance limit",
			blockNumber:              1000,
			lastL1AcceptedBlock:      uint64Ptr(100),
			defaultLIBDistanceToHead: 50,
			maxLIBDistanceToHead:     200,
			expected:                 800,
			description:              "Should apply max distance limit even with L1 block",
		},

		// Cases without L1 accepted block (using default distance)
		{
			name:                     "Default distance - normal case",
			blockNumber:              1000,
			lastL1AcceptedBlock:      nil,
			defaultLIBDistanceToHead: 100,
			maxLIBDistanceToHead:     200,
			expected:                 900,
			description:              "Should subtract default distance from block number",
		},
		{
			name:                     "Default distance - underflow protection",
			blockNumber:              50,
			lastL1AcceptedBlock:      nil,
			defaultLIBDistanceToHead: 100,
			maxLIBDistanceToHead:     200,
			expected:                 0,
			description:              "Should return 0 when default distance > block number",
		},
		{
			name:                     "Default distance - exact match",
			blockNumber:              100,
			lastL1AcceptedBlock:      nil,
			defaultLIBDistanceToHead: 100,
			maxLIBDistanceToHead:     200,
			expected:                 0,
			description:              "Should return 0 when default distance equals block number",
		},
		{
			name:                     "Default distance - block 0",
			blockNumber:              0,
			lastL1AcceptedBlock:      nil,
			defaultLIBDistanceToHead: 100,
			maxLIBDistanceToHead:     200,
			expected:                 0,
			description:              "Should return 0 for block 0",
		},

		// Max distance limit cases
		{
			name:                     "Max distance limit applied",
			blockNumber:              1000,
			lastL1AcceptedBlock:      nil,
			defaultLIBDistanceToHead: 100,
			maxLIBDistanceToHead:     50,
			expected:                 950,
			description:              "Should apply max distance limit",
		},
		{
			name:                     "Max distance limit - underflow protection",
			blockNumber:              50,
			lastL1AcceptedBlock:      nil,
			defaultLIBDistanceToHead: 200,
			maxLIBDistanceToHead:     100,
			expected:                 0,
			description:              "Should return 0 when max distance > block number",
		},
		{
			name:                     "Max distance limit disabled",
			blockNumber:              1000,
			lastL1AcceptedBlock:      nil,
			defaultLIBDistanceToHead: 50,
			maxLIBDistanceToHead:     0,
			expected:                 950,
			description:              "Should ignore max distance when set to 0",
		},
		{
			name:                     "Max distance equal to block number",
			blockNumber:              100,
			lastL1AcceptedBlock:      nil,
			defaultLIBDistanceToHead: 100,
			maxLIBDistanceToHead:     100,
			expected:                 0,
			description:              "Should return 0 when max distance equals block number",
		},

		// Edge cases with maximum uint64 values
		{
			name:                     "Maximum uint64 block number",
			blockNumber:              ^uint64(0), // Maximum uint64
			lastL1AcceptedBlock:      nil,
			defaultLIBDistanceToHead: 1000,
			maxLIBDistanceToHead:     2000,
			expected:                 ^uint64(0) - 1000,
			description:              "Should handle maximum uint64 values without overflow",
		},
		{
			name:                     "Maximum uint64 with L1 block",
			blockNumber:              ^uint64(0),
			lastL1AcceptedBlock:      uint64Ptr(^uint64(0) - 500),
			defaultLIBDistanceToHead: 1000,
			maxLIBDistanceToHead:     2000,
			expected:                 ^uint64(0) - 500,
			description:              "Should handle maximum uint64 L1 block",
		},
		{
			name:                     "Large numbers - no underflow",
			blockNumber:              1000000000000,
			lastL1AcceptedBlock:      nil,
			defaultLIBDistanceToHead: 999999999999,
			maxLIBDistanceToHead:     0,
			expected:                 1,
			description:              "Should handle large numbers correctly",
		},

		// Complex scenarios
		{
			name:                     "All parameters at boundary values",
			blockNumber:              1,
			lastL1AcceptedBlock:      nil,
			defaultLIBDistanceToHead: 1,
			maxLIBDistanceToHead:     1,
			expected:                 0,
			description:              "Should handle boundary conditions",
		},
		{
			name:                     "L1 block exactly at max distance",
			blockNumber:              1000,
			lastL1AcceptedBlock:      uint64Ptr(800),
			defaultLIBDistanceToHead: 50,
			maxLIBDistanceToHead:     200,
			expected:                 800,
			description:              "Should not modify L1 block when within max distance",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateLIBNum(tt.blockNumber, tt.lastL1AcceptedBlock, tt.defaultLIBDistanceToHead, tt.maxLIBDistanceToHead)
			require.Equal(t, tt.expected, result, tt.description)
		})
	}
}

func TestCalculateLIBNumDebug(t *testing.T) {
	// Debug the failing case: "L1 accepted block provided - normal case"
	blockNumber := uint64(1000)
	lastL1AcceptedBlock := uint64Ptr(500)
	defaultLIBDistanceToHead := uint64(100)
	maxLIBDistanceToHead := uint64(200)

	result := calculateLIBNum(blockNumber, lastL1AcceptedBlock, defaultLIBDistanceToHead, maxLIBDistanceToHead)

	t.Logf("blockNumber: %d", blockNumber)
	t.Logf("lastL1AcceptedBlock: %d", *lastL1AcceptedBlock)
	t.Logf("defaultLIBDistanceToHead: %d", defaultLIBDistanceToHead)
	t.Logf("maxLIBDistanceToHead: %d", maxLIBDistanceToHead)
	t.Logf("result: %d", result)
	t.Logf("blockNumber-lastL1AcceptedBlock: %d", blockNumber-*lastL1AcceptedBlock)
	t.Logf("maxLIBDistanceToHead check: %d > %d = %t", blockNumber-*lastL1AcceptedBlock, maxLIBDistanceToHead, (blockNumber-*lastL1AcceptedBlock) > maxLIBDistanceToHead)

	// Based on the logic:
	// 1. libNum = 500 (from L1)
	// 2. libNum <= blockNumber (500 <= 1000), so no safety adjustment
	// 3. maxLIBDistanceToHead != 0 and (1000-500) = 500 > 200, so libNum = 1000-200 = 800

	// The question is: should the max distance limit apply even when we have an L1 accepted block?
	// Looking at the original comment: "Limit lag of lib, whatever the method"
	// This suggests it should apply regardless of the source.
}

// Helper function to create a pointer to uint64
func uint64Ptr(val uint64) *uint64 {
	return &val
}

func TestConvertFelt(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "Test 1",
			input: "0x735596016a37ee972c42adef6a3cf628c19bb3794369c65d2c82ba034aecf2c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &felt.Felt{}
			var err error
			f, err = f.SetString(tt.input)
			require.NoError(t, err)

			result := convertFelt(f)

			f2 := &felt.Felt{}
			f2 = f2.SetBytes(result)

			require.Equal(t, tt.input, f2.String())
		})
	}
}
