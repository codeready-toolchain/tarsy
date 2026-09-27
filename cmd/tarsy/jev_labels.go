package main

import (
	"fmt"
	"os"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/labeling"
	"github.com/drpaneas/jev"
)

func newJEVLabelClassifier(cfg *config.ExperimentalJEVLabelsConfig) (*labeling.Classifier, error) {
	if cfg == nil || !cfg.Enabled {
		return nil, nil
	}
	client, err := jev.New(os.Getenv(cfg.APIKeyEnv), jev.WithModel(cfg.Model), jev.WithMaxRetries(0))
	if err != nil {
		return nil, fmt.Errorf("initialize experimental JEV labels (key env %s): %w", cfg.APIKeyEnv, err)
	}
	return labeling.New(client, cfg.MinConfidence), nil
}
