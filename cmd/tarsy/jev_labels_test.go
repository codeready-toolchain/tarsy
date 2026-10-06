package main

import (
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewJEVLabelClassifier(t *testing.T) {
	const keyEnv = "TARSY_TEST_JEV_KEY"
	t.Setenv(keyEnv, "")

	for _, cfg := range []*config.ExperimentalJEVLabelsConfig{nil, {APIKeyEnv: keyEnv}} {
		classifier, err := newJEVLabelClassifier(cfg)
		require.NoError(t, err)
		assert.Nil(t, classifier, "disabled experiment must not require a key")
	}

	cfg := &config.ExperimentalJEVLabelsConfig{
		Enabled: true, Model: "jev-1.13.0", APIKeyEnv: keyEnv, MinConfidence: 0.9,
	}
	classifier, err := newJEVLabelClassifier(cfg)
	require.Error(t, err)
	assert.Nil(t, classifier)

	t.Setenv(keyEnv, "offline-test-key")
	classifier, err = newJEVLabelClassifier(cfg)
	require.NoError(t, err)
	require.NotNil(t, classifier, "constructing the client does not contact TypeSafe")
}
