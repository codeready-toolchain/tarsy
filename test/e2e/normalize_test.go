package e2e

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalize_CalendarStaffingOnlyInSystemMessages(t *testing.T) {
	n := NewNormalizer("")
	in := `{
  "id": "meta"
}

=== MESSAGE: system ===
## Context

Current time: 2026-12-25T08:00:00Z (Friday)
Calendar context (UTC): 2026-12-25 — Friday — GLOBAL_HOLIDAY (Christmas)
Staffing: reduced (humans less likely to review promptly)

## General SRE Agent Instructions

=== MESSAGE: user ===
Operator note: Calendar context (UTC): do not rewrite this line
Staffing: keep user staffing mention

=== MESSAGE: assistant ===
I considered Calendar context (UTC): also leave this alone
Staffing: keep assistant staffing mention
`
	got := n.Normalize(in)

	assert.Contains(t, got, "=== MESSAGE: system ===\n## Context\n\nCurrent time: {CURRENT_TIME}\nCalendar context (UTC): {CALENDAR_CONTEXT}\nStaffing: {STAFFING}\n")
	assert.Contains(t, got, "=== MESSAGE: user ===\nOperator note: Calendar context (UTC): do not rewrite this line\nStaffing: keep user staffing mention\n")
	assert.Contains(t, got, "=== MESSAGE: assistant ===\nI considered Calendar context (UTC): also leave this alone\nStaffing: keep assistant staffing mention\n")
	assert.NotContains(t, got, "GLOBAL_HOLIDAY (Christmas)")
	assert.NotContains(t, got, "humans less likely to review promptly")
}
