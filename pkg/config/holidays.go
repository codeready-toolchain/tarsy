package config

// Holiday is a year-agnostic global holiday matched by MM-DD (UTC).
type Holiday struct {
	Date string // MM-DD
	Name string
}

// HolidayYAMLConfig is a single holiday entry from system.holidays in tarsy.yaml.
type HolidayYAMLConfig struct {
	Date string `yaml:"date"` // MM-DD
	Name string `yaml:"name"`
}

// DefaultHolidays returns the built-in global holiday list used when
// system.holidays is omitted or empty.
func DefaultHolidays() []Holiday {
	// Keep only broadly observed holidays across US, Canada, and much of Europe.
	return []Holiday{
		{Date: "01-01", Name: "New Year's Day"},
		{Date: "12-25", Name: "Christmas"},
	}
}
