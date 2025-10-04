package validation

import (
	"testing"

	"github.com/emersion/go-msgauth/authres"
	"github.com/mileusna/spf"
)

func TestConvertSPFResult(t *testing.T) {
	tests := []struct {
		name     string
		input    spf.Result
		expected authres.ResultValue
	}{
		{
			name:     "Pass",
			input:    spf.Pass,
			expected: authres.ResultPass,
		},
		{
			name:     "Fail",
			input:    spf.Fail,
			expected: authres.ResultFail,
		},
		{
			name:     "SoftFail",
			input:    spf.Softfail,
			expected: authres.ResultSoftFail,
		},
		{
			name:     "Neutral",
			input:    spf.Neutral,
			expected: authres.ResultNeutral,
		},
		{
			name:     "None",
			input:    spf.None,
			expected: authres.ResultNone,
		},
		{
			name:     "TempError",
			input:    spf.TempError,
			expected: authres.ResultNone, // TempError maps to None
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ConvertSPFResult(tt.input); got != tt.expected {
				t.Errorf("ConvertSPFResult() = %v, want %v", got, tt.expected)
			}
		})
	}
}
