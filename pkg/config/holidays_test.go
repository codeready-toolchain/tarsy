package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDefaultHolidays(t *testing.T) {
	got := DefaultHolidays()
	assert.Equal(t, []Holiday{
		{Date: "01-01", Name: "New Year's Day"},
		{Date: "12-25", Name: "Christmas"},
	}, got)

	// Callers must not observe shared mutable state if someone appends to the result.
	got[0].Name = "mutated"
	assert.Equal(t, "New Year's Day", DefaultHolidays()[0].Name)
}
