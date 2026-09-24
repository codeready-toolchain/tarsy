package e2e

import "testing"

func TestLessByCreatedAt(t *testing.T) {
	t.Parallel()

	// Later instant whose RFC3339Nano form sorts first as a string.
	const (
		earlier = "2026-09-24T22:24:04.1Z"
		later   = "2026-09-24T22:24:04.1001Z"
	)
	if earlier <= later {
		t.Fatal("fixture no longer demonstrates the string-order inversion")
	}

	tests := []struct {
		name string
		a, b string
		tieA string
		tieB string
		want bool
	}{
		{name: "later fractional prefix sorts after earlier instant", a: earlier, b: later, want: true},
		{name: "earlier instant does not sort after later", a: later, b: earlier, want: false},
		{name: "equal instant tiebreaks by server name", a: earlier, b: earlier, tieA: "", tieB: "test-mcp", want: true},
		{name: "equal instant non-empty server sorts after", a: earlier, b: earlier, tieA: "test-mcp", tieB: "", want: false},
		{name: "same instant different precision uses tiebreak", a: "2026-09-24T22:24:04.1Z", b: "2026-09-24T22:24:04.100000000Z", tieA: "a", tieB: "b", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := lessByCreatedAt(tt.a, tt.b, tt.tieA, tt.tieB); got != tt.want {
				t.Fatalf("lessByCreatedAt(%q, %q, %q, %q) = %v, want %v", tt.a, tt.b, tt.tieA, tt.tieB, got, tt.want)
			}
		})
	}
}
