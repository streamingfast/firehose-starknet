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
		name                string
		blockNumber         uint64
		lastL1AcceptedBlock uint64
		expected            uint64
		description         string
	}{
		// Basic cases - L1 accepted block is older than current block
		{
			name:                "L1 block older than current - normal case",
			blockNumber:         1000,
			lastL1AcceptedBlock: 500,
			expected:            999,
			description:         "Should use blockNumber-1 when L1 block is older than current block",
		},
		{
			name:                "L1 block much older than current",
			blockNumber:         1000000,
			lastL1AcceptedBlock: 100,
			expected:            999999,
			description:         "Should use blockNumber-1 when L1 block is much older than current",
		},

		// Cases where L1 accepted block is newer than or equal to current block
		{
			name:                "L1 block equal to current - use block-1",
			blockNumber:         1000,
			lastL1AcceptedBlock: 1000,
			expected:            999,
			description:         "Should use blockNumber-1 when L1 block equals current block",
		},
		{
			name:                "L1 block newer than current - use L1 block",
			blockNumber:         1000,
			lastL1AcceptedBlock: 1500,
			expected:            1500,
			description:         "Should use L1 accepted block when L1 block is newer than current block",
		},

		// Edge cases with block 0
		{
			name:                "Block 0 with L1 block 0",
			blockNumber:         0,
			lastL1AcceptedBlock: 0,
			expected:            0,
			description:         "Should return 0 when both blocks are 0",
		},
		{
			name:                "Block 0 with newer L1 block",
			blockNumber:         0,
			lastL1AcceptedBlock: 100,
			expected:            100,
			description:         "Should use L1 block when current block is 0 and L1 is newer",
		},
		{
			name:                "Block 1 with L1 block 1",
			blockNumber:         1,
			lastL1AcceptedBlock: 1,
			expected:            0,
			description:         "Should return 0 when block 1 >= L1 block 1",
		},
		{
			name:                "Block 1 with L1 block 0",
			blockNumber:         1,
			lastL1AcceptedBlock: 0,
			expected:            0,
			description:         "Should use blockNumber-1=0 when L1 block is older and blockNumber>0",
		},

		// Maximum uint64 values to test overflow protection
		{
			name:                "Maximum uint64 block with smaller L1",
			blockNumber:         ^uint64(0), // Maximum uint64
			lastL1AcceptedBlock: 1000,
			expected:            ^uint64(0) - 1,
			description:         "Should use blockNumber-1 when L1 block is smaller",
		},
		{
			name:                "Maximum uint64 block with max L1",
			blockNumber:         ^uint64(0),
			lastL1AcceptedBlock: ^uint64(0),
			expected:            ^uint64(0) - 1,
			description:         "Should handle maximum uint64 values without overflow",
		},
		{
			name:                "Large block with larger L1 - use L1 block",
			blockNumber:         ^uint64(0) - 1000,
			lastL1AcceptedBlock: ^uint64(0),
			expected:            ^uint64(0),
			description:         "Should use L1 block when it's larger than current block",
		},

		// Boundary conditions
		{
			name:                "Block 2 with L1 block 2",
			blockNumber:         2,
			lastL1AcceptedBlock: 2,
			expected:            1,
			description:         "Should return 1 when block 2 >= L1 block 2",
		},
		{
			name:                "Large numbers - L1 block older",
			blockNumber:         1000000000000,
			lastL1AcceptedBlock: 999999999999,
			expected:            999999999999,
			description:         "Should use blockNumber-1 when L1 block is older",
		},
		{
			name:                "Large numbers - L1 block newer",
			blockNumber:         999999999999,
			lastL1AcceptedBlock: 1000000000000,
			expected:            1000000000000,
			description:         "Should use L1 block when it's newer than current block",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateLIBNum(tt.blockNumber, tt.lastL1AcceptedBlock)
			require.Equal(t, tt.expected, result, tt.description)
		})
	}
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
