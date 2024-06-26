package rpc

import (
	"testing"

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
