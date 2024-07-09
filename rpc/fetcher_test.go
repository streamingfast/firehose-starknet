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
