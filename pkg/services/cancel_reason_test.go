package services

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeCancelReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    *string
		wantErr bool
	}{
		{name: "empty is omitted", input: ""},
		{name: "whitespace is omitted", input: "   \n\t  "},
		{
			name:  "trims surrounding whitespace",
			input: "  duplicate alert  ",
			want:  strPtr("duplicate alert"),
		},
		{
			name:  "exactly 500 runes is accepted",
			input: strings.Repeat("a", MaxCancelReasonLength),
			want:  strPtr(strings.Repeat("a", MaxCancelReasonLength)),
		},
		{
			name:    "501 runes is rejected",
			input:   strings.Repeat("a", MaxCancelReasonLength+1),
			wantErr: true,
		},
		{
			name:  "multi-byte runes count as one",
			input: strings.Repeat("🔥", MaxCancelReasonLength),
			want:  strPtr(strings.Repeat("🔥", MaxCancelReasonLength)),
		},
		{
			name:    "501 multi-byte runes is rejected",
			input:   strings.Repeat("🔥", MaxCancelReasonLength+1),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeCancelReason(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				assert.True(t, IsValidationError(err))
				var ve *ValidationError
				require.ErrorAs(t, err, &ve)
				assert.Equal(t, "reason", ve.Field)
				assert.Equal(t, "must not exceed 500 characters", ve.Message)
				assert.Greater(t, utf8.RuneCountInString(tt.input), MaxCancelReasonLength)
				return
			}
			require.NoError(t, err)
			if tt.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tt.want, *got)
		})
	}
}
