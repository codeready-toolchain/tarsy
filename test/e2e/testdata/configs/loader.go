// Package configs provides test configuration loading for e2e tests.
// Configs are stored as YAML files (the same format as production) and loaded
// through the production config.Initialize path, ensuring built-in agents,
// merge logic, and validation are all exercised.
package configs

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/config"
)

// configsDir returns the absolute path to the configs testdata directory.
// Uses runtime.Caller so it works regardless of the working directory.
func configsDir() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Dir(thisFile)
}

// Load loads a named test configuration using the production config.Initialize path.
// The name corresponds to a subdirectory under testdata/configs/ containing
// tarsy.yaml and llm-providers.yaml.
//
// Available configs: pipeline, full-flow, two-stage-fail-fast,
// parallel-any, parallel-all, replica, chat, forced-conclusion.
func Load(t *testing.T, name string) *config.Config {
	t.Helper()
	dir := filepath.Join(configsDir(), name)
	cfg, err := config.Initialize(context.Background(), dir)
	require.NoError(t, err, "failed to load test config %q from %s", name, dir)
	return cfg
}
