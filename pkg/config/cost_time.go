package config

import (
	"fmt"
	"time"
)

// ParseCostTime parses a promotion window bound as RFC3339 (any offset → UTC)
// or as YYYY-MM-DD (interpreted as that day's 00:00:00 UTC).
func ParseCostTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC(), nil
	}
	t, err := time.ParseInLocation("2006-01-02", s, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("want RFC3339 or YYYY-MM-DD: %w", err)
	}
	return t, nil
}

// ParsePromotionWindow parses optional start and required end for a promotion.
// Empty start means already active (returned start is nil).
func ParsePromotionWindow(startRaw, endRaw string) (start *time.Time, end time.Time, err error) {
	if endRaw == "" {
		return nil, time.Time{}, fmt.Errorf("end is required")
	}
	end, err = ParseCostTime(endRaw)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("end: %w", err)
	}
	if startRaw == "" {
		return nil, end, nil
	}
	s, err := ParseCostTime(startRaw)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("start: %w", err)
	}
	return &s, end, nil
}
