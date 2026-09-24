package services

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxCancelReasonLength is the maximum number of Unicode runes allowed in a
// cancel reason. Empty / whitespace-only reasons are treated as omitted.
const MaxCancelReasonLength = 500

// NormalizeCancelReason trims s and returns nil when the result is empty.
// Reasons longer than MaxCancelReasonLength runes return a ValidationError.
func NormalizeCancelReason(s string) (*string, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(trimmed) > MaxCancelReasonLength {
		return nil, NewValidationError("reason",
			fmt.Sprintf("must not exceed %d characters", MaxCancelReasonLength))
	}
	return &trimmed, nil
}
