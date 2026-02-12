package e2e

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var updateGolden = flag.Bool("update", false, "update golden files")

// AssertGolden compares actual output against a golden file.
// If -update flag is set, writes actual to the golden file instead.
func AssertGolden(t *testing.T, goldenPath string, actual []byte) {
	t.Helper()

	if *updateGolden {
		dir := filepath.Dir(goldenPath)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(goldenPath, actual, 0o644))
		t.Logf("Updated golden file: %s", goldenPath)
		return
	}

	expected, err := os.ReadFile(goldenPath)
	require.NoError(t, err, "golden file not found: %s (run with -update to create)", goldenPath)
	assert.Equal(t, string(expected), string(actual), "golden mismatch: %s", goldenPath)
}

// AssertGoldenJSON normalizes JSON and compares against a golden file.
// The actual value is marshalled with sorted keys and indentation.
func AssertGoldenJSON(t *testing.T, goldenPath string, actual interface{}, normalizer *Normalizer) {
	t.Helper()

	data, err := json.MarshalIndent(actual, "", "  ")
	require.NoError(t, err)

	if normalizer != nil {
		data = normalizer.NormalizeBytes(data)
	}

	// Ensure trailing newline for clean diffs.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}

	AssertGolden(t, goldenPath, data)
}

// AssertGoldenEvents normalizes and compares a WebSocket event sequence.
// Events are first filtered and projected via FilterEventsForGolden,
// then serialized as line-delimited JSON.
func AssertGoldenEvents(t *testing.T, goldenPath string, events []WSEvent, normalizer *Normalizer) {
	t.Helper()

	filtered := FilterEventsForGolden(events)

	var lines []byte
	for _, evt := range filtered {
		data, err := json.Marshal(evt)
		require.NoError(t, err)
		lines = append(lines, data...)
		lines = append(lines, '\n')
	}

	if normalizer != nil {
		lines = normalizer.NormalizeBytes(lines)
	}

	AssertGolden(t, goldenPath, lines)
}

// goldenDir returns the path to the testdata/golden directory for a scenario.
func goldenDir(scenario string) string {
	return filepath.Join("testdata", "golden", scenario)
}

// GoldenPath returns the path to a specific golden file for a scenario.
func GoldenPath(scenario, filename string) string {
	return filepath.Join(goldenDir(scenario), filename)
}
