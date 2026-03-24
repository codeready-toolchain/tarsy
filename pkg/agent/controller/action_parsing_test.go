package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractActionsTaken(t *testing.T) {
	tests := []struct {
		name            string
		text            string
		wantTaken       bool
		wantAnalysis    string
		wantErr         bool
		wantErrContains string
	}{
		{
			name:         "YES with analysis",
			text:         "Investigation findings here.\n\nActions section added.\nYES",
			wantTaken:    true,
			wantAnalysis: "Investigation findings here.\n\nActions section added.",
		},
		{
			name:         "NO with analysis",
			text:         "Investigation findings here.\n\nNo actions warranted.\nNO",
			wantTaken:    false,
			wantAnalysis: "Investigation findings here.\n\nNo actions warranted.",
		},
		{
			name:         "case insensitive yes",
			text:         "Report text.\nyes",
			wantTaken:    true,
			wantAnalysis: "Report text.",
		},
		{
			name:         "case insensitive No",
			text:         "Report text.\nNo",
			wantTaken:    false,
			wantAnalysis: "Report text.",
		},
		{
			name:         "marker with trailing whitespace",
			text:         "Report.\nYES   \n\n",
			wantTaken:    true,
			wantAnalysis: "Report.",
		},
		{
			name:         "marker with leading whitespace",
			text:         "Report.\n  NO",
			wantTaken:    false,
			wantAnalysis: "Report.",
		},
		{
			name:         "marker only - no analysis",
			text:         "YES",
			wantTaken:    true,
			wantAnalysis: "",
		},
		{
			name:            "empty text",
			text:            "",
			wantErr:         true,
			wantErrContains: "empty response text",
		},
		{
			name:            "no marker on last line",
			text:            "Investigation report.\nFinal conclusion.",
			wantErr:         true,
			wantErrContains: "no YES/NO marker found on last line",
		},
		{
			name:            "marker with extra text on same line",
			text:            "Report.\nYES -- done",
			wantErr:         true,
			wantErrContains: "no YES/NO marker found on last line",
		},
		{
			name:            "whitespace only",
			text:            "   \n\n  ",
			wantErr:         true,
			wantErrContains: "empty response text",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			taken, analysis, err := ExtractActionsTaken(tt.text)

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrContains)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantTaken, taken)
			assert.Equal(t, tt.wantAnalysis, analysis)
		})
	}
}
