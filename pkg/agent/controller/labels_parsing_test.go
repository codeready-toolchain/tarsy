package controller

import (
	"strings"
	"testing"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func exclusiveMap() config.LabelMap {
	return config.LabelMap{
		Multi: false,
		Labels: []config.LabelSpec{
			{Label: "watch", Description: "w"},
			{Label: "action", Description: "a"},
			{Label: "noise", Description: "n"},
		},
	}
}

func customExclusiveMap() config.LabelMap {
	return config.LabelMap{
		Multi: false,
		Labels: []config.LabelSpec{
			{Label: "monitor", Description: "m"},
			{Label: "page", Description: "p"},
			{Label: "false_positive", Description: "fp"},
		},
	}
}

func multiMap() config.LabelMap {
	return config.LabelMap{
		Multi: true,
		Labels: []config.LabelSpec{
			{Label: "watch", Description: "w"},
			{Label: "page", Description: "p"},
			{Label: "modify_detection_rules", Description: "d"},
		},
	}
}

func TestExtractLabels(t *testing.T) {
	builtin := exclusiveMap()
	custom := customExclusiveMap()
	multi := multiMap()

	tests := []struct {
		name       string
		text       string
		m          config.LabelMap
		wantLabels []string
		wantClean  string
		trailer    bool
		valid      bool
	}{
		{
			name:       "empty text",
			text:       "",
			m:          builtin,
			wantLabels: []string{},
			wantClean:  "",
			valid:      true,
		},
		{
			name:       "whitespace only",
			text:       " \n\t",
			m:          builtin,
			wantLabels: []string{},
			wantClean:  "",
			valid:      true,
		},
		{
			name:       "no trailer",
			text:       "Pod OOM killed.",
			m:          builtin,
			wantLabels: []string{},
			wantClean:  "Pod OOM killed.",
			valid:      true,
		},
		{
			name:       "trailer only",
			text:       "LABELS: watch",
			m:          builtin,
			wantLabels: []string{"watch"},
			wantClean:  "",
			trailer:    true,
			valid:      true,
		},
		{
			name:       "lowercase prefix",
			text:       "Look again if it persists.\nlabels: watch",
			m:          builtin,
			wantLabels: []string{"watch"},
			wantClean:  "Look again if it persists.",
			trailer:    true,
			valid:      true,
		},
		{
			name:       "no trailer trailing whitespace",
			text:       "Pod OOM killed.\n\n",
			m:          builtin,
			wantLabels: []string{},
			wantClean:  "Pod OOM killed.",
			valid:      true,
		},
		{
			name:       "LABELS not last line is not a trailer",
			text:       "LABELS: action\nStill more summary.",
			m:          builtin,
			wantLabels: []string{},
			wantClean:  "LABELS: action\nStill more summary.",
			valid:      true,
		},
		{
			name:       "LABEL without S is not a trailer",
			text:       "Pod recovered.\nLABEL: noise",
			m:          builtin,
			wantLabels: []string{},
			wantClean:  "Pod recovered.\nLABEL: noise",
			valid:      true,
		},
		{
			name:       "empty LABELS remainder",
			text:       "Pod recovered.\nLABELS:",
			m:          builtin,
			wantLabels: []string{},
			wantClean:  "Pod recovered.",
			trailer:    true,
			valid:      true,
		},
		{
			name:       "empty LABELS with spaces and trailing comma",
			text:       "Pod recovered.\nLABELS:  ,",
			m:          builtin,
			wantLabels: []string{},
			wantClean:  "Pod recovered.",
			trailer:    true,
			valid:      true,
		},
		{
			name:       "LABELS:page no space",
			text:       "Human must restart the workload.\nLABELS:page",
			m:          custom,
			wantLabels: []string{"page"},
			wantClean:  "Human must restart the workload.",
			trailer:    true,
			valid:      true,
		},
		{
			name:       "wrapping markdown on the line",
			text:       "Human must restart the workload.\n**LABELS: page**",
			m:          custom,
			wantLabels: []string{"page"},
			wantClean:  "Human must restart the workload.",
			trailer:    true,
			valid:      true,
		},
		{
			name:    "per-token markdown is leftover junk",
			text:    "Human must restart the workload.\nLABELS: **page**",
			m:       custom,
			trailer: true,
			valid:   false,
		},
		{
			name:       "trailing comma",
			text:       "Close this alert.\nLABELS: noise,",
			m:          builtin,
			wantLabels: []string{"noise"},
			wantClean:  "Close this alert.",
			trailer:    true,
			valid:      true,
		},
		{
			name:       "one label",
			text:       "Look again if it persists.\nLABELS: watch",
			m:          builtin,
			wantLabels: []string{"watch"},
			wantClean:  "Look again if it persists.",
			trailer:    true,
			valid:      true,
		},
		{
			name:       "duplicate names with multi false is one name",
			text:       "Look again.\nLABELS: watch, watch",
			m:          builtin,
			wantLabels: []string{"watch"},
			wantClean:  "Look again.",
			trailer:    true,
			valid:      true,
		},
		{
			name:       "multi subset map order",
			text:       "Tune rules and keep watching.\nLABELS: modify_detection_rules, watch",
			m:          multi,
			wantLabels: []string{"watch", "modify_detection_rules"},
			wantClean:  "Tune rules and keep watching.",
			trailer:    true,
			valid:      true,
		},
		{
			name:       "case insensitive",
			text:       "Close this alert.\nLABELS: NOISE",
			m:          builtin,
			wantLabels: []string{"noise"},
			wantClean:  "Close this alert.",
			trailer:    true,
			valid:      true,
		},
		{
			name:       "trailing period on token",
			text:       "Close this alert.\nLABELS: noise.",
			m:          builtin,
			wantLabels: []string{"noise"},
			wantClean:  "Close this alert.",
			trailer:    true,
			valid:      true,
		},
		{
			name:       "trailing semicolon on token",
			text:       "Close this alert.\nLABELS: noise;",
			m:          builtin,
			wantLabels: []string{"noise"},
			wantClean:  "Close this alert.",
			trailer:    true,
			valid:      true,
		},
		{
			name:       "underscore wrapping",
			text:       "Intervene now.\n_LABELS: action_",
			m:          builtin,
			wantLabels: []string{"action"},
			wantClean:  "Intervene now.",
			trailer:    true,
			valid:      true,
		},
		{
			name:    "unknown name",
			text:    "Something.\nLABELS: page",
			m:       builtin,
			trailer: true,
			valid:   false,
		},
		{
			name:    "leftover junk in token",
			text:    "Something.\nLABELS: page please",
			m:       custom,
			trailer: true,
			valid:   false,
		},
		{
			name:    "multi false with two distinct names",
			text:    "Mixed.\nLABELS: watch, noise",
			m:       builtin,
			trailer: true,
			valid:   false,
		},
		{
			name:       "spaces around colon",
			text:       "Intervene now.\nLABELS  :  action",
			m:          builtin,
			wantLabels: []string{"action"},
			wantClean:  "Intervene now.",
			trailer:    true,
			valid:      true,
		},
		{
			name:       "backtick wrapping",
			text:       "Intervene now.\n`LABELS: action`",
			m:          builtin,
			wantLabels: []string{"action"},
			wantClean:  "Intervene now.",
			trailer:    true,
			valid:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractLabels(tt.text, tt.m)
			assert.Equal(t, tt.valid, got.Valid)
			assert.Equal(t, tt.trailer, got.HasTrailer)
			if !tt.valid {
				assert.Equal(t, strings.TrimRight(tt.text, "\n\r \t"), got.Cleaned)
				return
			}
			require.NotNil(t, got.Labels)
			assert.Equal(t, tt.wantLabels, got.Labels)
			assert.Equal(t, tt.wantClean, got.Cleaned)
		})
	}
}

func TestCanonicalLabelsPtr(t *testing.T) {
	t.Run("nil becomes empty non-nil slice", func(t *testing.T) {
		got := canonicalLabelsPtr(nil)
		require.NotNil(t, got)
		require.NotNil(t, *got)
		assert.Empty(t, *got)
	})

	t.Run("clones so caller mutation does not alias", func(t *testing.T) {
		in := []string{"page"}
		got := canonicalLabelsPtr(in)
		require.NotNil(t, got)
		assert.Equal(t, []string{"page"}, *got)
		in[0] = "mutated"
		assert.Equal(t, []string{"page"}, *got)
	})
}
