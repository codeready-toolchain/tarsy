package prompt

import (
	"strings"
	"testing"
	"time"

	"github.com/codeready-toolchain/tarsy/pkg/agent"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func freezeUTC(t *testing.T, now time.Time) {
	t.Helper()
	prev := nowUTC
	nowUTC = func() time.Time { return now }
	t.Cleanup(func() { nowUTC = prev })
}

func TestFormatCurrentTimeSection(t *testing.T) {
	defaultHolidays := config.DefaultHolidays()
	// Product list shaped like sandbox-sre Christmas break (Eve → New Year's Day).
	christmasBreak := []config.Holiday{
		{Date: "12-24", Name: "Christmas Eve"},
		{Date: "12-25", Name: "Christmas"},
		{Date: "12-26", Name: "Christmas holiday"},
		{Date: "12-27", Name: "Christmas holiday"},
		{Date: "12-28", Name: "Christmas holiday"},
		{Date: "12-29", Name: "Christmas holiday"},
		{Date: "12-30", Name: "Christmas holiday"},
		{Date: "12-31", Name: "New Year's Eve"},
		{Date: "01-01", Name: "New Year's Day"},
	}

	tests := []struct {
		name     string
		now      time.Time
		holidays []config.Holiday
		want     string
	}{
		{
			name:     "weekday normal staffing",
			now:      time.Date(2026, 8, 12, 15, 30, 0, 0, time.UTC), // Wednesday
			holidays: defaultHolidays,
			want: strings.Join([]string{
				"## Context",
				"",
				"Current time: 2026-08-12T15:30:00Z (Wednesday)",
				"Calendar context (UTC): 2026-08-12 — Wednesday — WEEKDAY",
				"Staffing: normal",
			}, "\n"),
		},
		{
			name:     "saturday weekend reduced staffing",
			now:      time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), // Saturday
			holidays: defaultHolidays,
			want: strings.Join([]string{
				"## Context",
				"",
				"Current time: 2026-08-01T10:00:00Z (Saturday)",
				"Calendar context (UTC): 2026-08-01 — Saturday — WEEKEND",
				"Staffing: reduced (humans less likely to review promptly)",
			}, "\n"),
		},
		{
			name:     "sunday weekend reduced staffing",
			now:      time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC), // Sunday
			holidays: defaultHolidays,
			want: strings.Join([]string{
				"## Context",
				"",
				"Current time: 2026-08-02T10:00:00Z (Sunday)",
				"Calendar context (UTC): 2026-08-02 — Sunday — WEEKEND",
				"Staffing: reduced (humans less likely to review promptly)",
			}, "\n"),
		},
		{
			name:     "default christmas on weekday",
			now:      time.Date(2026, 12, 25, 8, 0, 0, 0, time.UTC), // Friday
			holidays: defaultHolidays,
			want: strings.Join([]string{
				"## Context",
				"",
				"Current time: 2026-12-25T08:00:00Z (Friday)",
				"Calendar context (UTC): 2026-12-25 — Friday — GLOBAL_HOLIDAY (Christmas)",
				"Staffing: reduced (humans less likely to review promptly)",
			}, "\n"),
		},
		{
			name:     "holiday takes priority over weekend",
			now:      time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC), // Sunday New Year's
			holidays: defaultHolidays,
			want: strings.Join([]string{
				"## Context",
				"",
				"Current time: 2023-01-01T12:00:00Z (Sunday)",
				"Calendar context (UTC): 2023-01-01 — Sunday — GLOBAL_HOLIDAY (New Year's Day)",
				"Staffing: reduced (humans less likely to review promptly)",
			}, "\n"),
		},
		{
			name: "defaults miss mid christmas break weekday",
			// Monday 2026-12-28 is between Christmas and New Year; not in DefaultHolidays.
			now:      time.Date(2026, 12, 28, 9, 0, 0, 0, time.UTC),
			holidays: defaultHolidays,
			want: strings.Join([]string{
				"## Context",
				"",
				"Current time: 2026-12-28T09:00:00Z (Monday)",
				"Calendar context (UTC): 2026-12-28 — Monday — WEEKDAY",
				"Staffing: normal",
			}, "\n"),
		},
		{
			name:     "product christmas break covers midweek day",
			now:      time.Date(2026, 12, 28, 9, 0, 0, 0, time.UTC), // Monday
			holidays: christmasBreak,
			want: strings.Join([]string{
				"## Context",
				"",
				"Current time: 2026-12-28T09:00:00Z (Monday)",
				"Calendar context (UTC): 2026-12-28 — Monday — GLOBAL_HOLIDAY (Christmas holiday)",
				"Staffing: reduced (humans less likely to review promptly)",
			}, "\n"),
		},
		{
			name:     "product christmas eve",
			now:      time.Date(2026, 12, 24, 18, 0, 0, 0, time.UTC), // Thursday
			holidays: christmasBreak,
			want: strings.Join([]string{
				"## Context",
				"",
				"Current time: 2026-12-24T18:00:00Z (Thursday)",
				"Calendar context (UTC): 2026-12-24 — Thursday — GLOBAL_HOLIDAY (Christmas Eve)",
				"Staffing: reduced (humans less likely to review promptly)",
			}, "\n"),
		},
		{
			name:     "custom list replaces christmas",
			now:      time.Date(2026, 12, 25, 8, 0, 0, 0, time.UTC),
			holidays: []config.Holiday{{Date: "07-04", Name: "Independence Day"}},
			want: strings.Join([]string{
				"## Context",
				"",
				"Current time: 2026-12-25T08:00:00Z (Friday)",
				"Calendar context (UTC): 2026-12-25 — Friday — WEEKDAY",
				"Staffing: normal",
			}, "\n"),
		},
		{
			name:     "custom holiday on weekend still GLOBAL_HOLIDAY",
			now:      time.Date(2026, 7, 4, 8, 0, 0, 0, time.UTC), // Saturday
			holidays: []config.Holiday{{Date: "07-04", Name: "Independence Day"}},
			want: strings.Join([]string{
				"## Context",
				"",
				"Current time: 2026-07-04T08:00:00Z (Saturday)",
				"Calendar context (UTC): 2026-07-04 — Saturday — GLOBAL_HOLIDAY (Independence Day)",
				"Staffing: reduced (humans less likely to review promptly)",
			}, "\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "holiday takes priority over weekend" {
				require.Equal(t, time.Sunday, tt.now.Weekday())
			}
			assert.Equal(t, tt.want, formatCurrentTimeSection(tt.now, tt.holidays))
		})
	}
}

func TestFormatCurrentTimeSection_ChristmasBreakAllDays(t *testing.T) {
	christmasBreak := []config.Holiday{
		{Date: "12-24", Name: "Christmas Eve"},
		{Date: "12-25", Name: "Christmas"},
		{Date: "12-26", Name: "Christmas holiday"},
		{Date: "12-27", Name: "Christmas holiday"},
		{Date: "12-28", Name: "Christmas holiday"},
		{Date: "12-29", Name: "Christmas holiday"},
		{Date: "12-30", Name: "Christmas holiday"},
		{Date: "12-31", Name: "New Year's Eve"},
		{Date: "01-01", Name: "New Year's Day"},
	}

	// Walk Christmas Eve 2026 through New Year's Day 2027 (UTC).
	for d := time.Date(2026, 12, 24, 12, 0, 0, 0, time.UTC); !d.After(time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)); d = d.Add(24 * time.Hour) {
		t.Run(d.Format("2006-01-02"), func(t *testing.T) {
			got := formatCurrentTimeSection(d, christmasBreak)
			assert.Contains(t, got, "GLOBAL_HOLIDAY (")
			assert.Contains(t, got, "Staffing: reduced (humans less likely to review promptly)")
			assert.NotContains(t, got, "Staffing: normal")
			assert.NotContains(t, got, "— WEEKDAY")
			assert.NotContains(t, got, "— WEEKEND")
		})
	}
}

func TestComposeInstructions_Tier0CalendarUsesBuilderHolidays(t *testing.T) {
	freezeUTC(t, time.Date(2026, 12, 28, 9, 0, 0, 0, time.UTC)) // Monday mid Christmas break

	registry := newTestMCPRegistry(nil)
	builder := NewPromptBuilder(registry, []config.Holiday{
		{Date: "12-28", Name: "Christmas holiday"},
	})
	result := builder.ComposeInstructions(newTestExecCtx())

	assert.Contains(t, result, "Calendar context (UTC): 2026-12-28 — Monday — GLOBAL_HOLIDAY (Christmas holiday)")
	assert.Contains(t, result, "Staffing: reduced (humans less likely to review promptly)")
	assert.True(t, strings.Index(result, "## Context") < strings.Index(result, "General SRE Agent Instructions"),
		"Tier 0 Context should precede Tier 1 general instructions")
}

func TestComposeInstructions_Tier0UsesDefaultHolidaysWhenNil(t *testing.T) {
	freezeUTC(t, time.Date(2026, 12, 25, 8, 0, 0, 0, time.UTC))

	builder := NewPromptBuilder(newTestMCPRegistry(nil), nil)
	result := builder.ComposeInstructions(newTestExecCtx())

	assert.Contains(t, result, "GLOBAL_HOLIDAY (Christmas)")
	assert.Contains(t, result, "Staffing: reduced")
}

func TestComposeChatInstructions_Tier0Calendar(t *testing.T) {
	freezeUTC(t, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC)) // Saturday

	builder := NewPromptBuilder(newTestMCPRegistry(nil), config.DefaultHolidays())
	result := builder.ComposeChatInstructions(newTestExecCtx())

	assert.Contains(t, result, "Calendar context (UTC): 2026-08-01 — Saturday — WEEKEND")
	assert.Contains(t, result, "Staffing: reduced")
}

func TestBuildActionMessages_IncludesTier0Calendar(t *testing.T) {
	freezeUTC(t, time.Date(2026, 12, 24, 18, 0, 0, 0, time.UTC)) // Christmas Eve

	builder := NewPromptBuilder(newTestMCPRegistry(nil), []config.Holiday{
		{Date: "12-24", Name: "Christmas Eve"},
	})
	execCtx := newTestExecCtx()
	execCtx.Config.Type = config.AgentTypeAction
	execCtx.AlertData = `{"alertname":"test"}`
	execCtx.AlertType = "SecurityInvestigation"

	messages := builder.buildActionMessages(execCtx, "prior stage findings")
	require.NotEmpty(t, messages)
	assert.Equal(t, agent.RoleSystem, messages[0].Role)
	assert.Contains(t, messages[0].Content, "GLOBAL_HOLIDAY (Christmas Eve)")
	assert.Contains(t, messages[0].Content, "Staffing: reduced")
}
