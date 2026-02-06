package config

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidationErrorError(t *testing.T) {
	baseErr := errors.New("base error")
	
	tests := []struct {
		name      string
		err       *ValidationError
		contains  []string
	}{
		{
			name: "full error",
			err:  NewValidationError("agent", "test-agent", "mcp_servers", baseErr),
			contains: []string{
				"agent",
				"test-agent",
				"mcp_servers",
				"base error",
			},
		},
		{
			name: "chain error",
			err:  NewValidationError("chain", "k8s-chain", "stages", errors.New("invalid stage")),
			contains: []string{
				"chain",
				"k8s-chain",
				"stages",
				"invalid stage",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errStr := tt.err.Error()
			for _, substr := range tt.contains {
				assert.Contains(t, errStr, substr)
			}
		})
	}
}

func TestValidationErrorUnwrap(t *testing.T) {
	baseErr := errors.New("base error")
	validationErr := NewValidationError("test", "test-id", "field", baseErr)
	
	unwrapped := validationErr.Unwrap()
	assert.Equal(t, baseErr, unwrapped)
	assert.True(t, errors.Is(validationErr, baseErr))
}

func TestLoadErrorError(t *testing.T) {
	tests := []struct {
		name     string
		err      *LoadError
		contains []string
	}{
		{
			name: "file load error",
			err: &LoadError{
				File: "tarsy.yaml",
				Err:  errors.New("file not found"),
			},
			contains: []string{
				"failed to load",
				"tarsy.yaml",
				"file not found",
			},
		},
		{
			name: "parse error",
			err: &LoadError{
				File: "llm-providers.yaml",
				Err:  errors.New("yaml: unmarshal error"),
			},
			contains: []string{
				"failed to load",
				"llm-providers.yaml",
				"unmarshal error",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errStr := tt.err.Error()
			for _, substr := range tt.contains {
				assert.Contains(t, errStr, substr)
			}
		})
	}
}

func TestLoadErrorUnwrap(t *testing.T) {
	baseErr := errors.New("base error")
	loadErr := &LoadError{
		File: "test.yaml",
		Err:  baseErr,
	}
	
	unwrapped := loadErr.Unwrap()
	assert.Equal(t, baseErr, unwrapped)
	assert.True(t, errors.Is(loadErr, baseErr))
}
