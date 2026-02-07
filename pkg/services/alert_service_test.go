package services

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestAlertService(t *testing.T, client *database.Client) *AlertService {
	t.Helper()

	// Create chain registry with test chains
	chainRegistry := config.NewChainRegistry(map[string]*config.ChainConfig{
		"k8s-analysis": {
			AlertTypes:  []string{"pod-crash"},
			Description: "Kubernetes pod crash analysis",
			Stages: []config.StageConfig{
				{
					Name:   "analysis",
					Agents: []config.StageAgentConfig{{Name: "KubernetesAgent"}},
				},
			},
		},
		"default-chain": {
			AlertTypes:  []string{"generic"},
			Description: "Default generic analysis",
			Stages: []config.StageConfig{
				{
					Name:   "analysis",
					Agents: []config.StageAgentConfig{{Name: "GenericAgent"}},
				},
			},
		},
	})

	defaults := &config.Defaults{
		AlertType: "generic",
	}

	return NewAlertService(client.Client, chainRegistry, defaults)
}

func TestNewAlertService(t *testing.T) {
	client := testdb.NewTestClient(t)
	chainRegistry := config.NewChainRegistry(map[string]*config.ChainConfig{})
	defaults := &config.Defaults{AlertType: "generic"}

	t.Run("panics when chainRegistry is nil", func(t *testing.T) {
		assert.Panics(t, func() {
			NewAlertService(client.Client, nil, defaults)
		})
	})

	t.Run("panics when defaults is nil", func(t *testing.T) {
		assert.Panics(t, func() {
			NewAlertService(client.Client, chainRegistry, nil)
		})
	})

	t.Run("succeeds with valid inputs", func(t *testing.T) {
		service := NewAlertService(client.Client, chainRegistry, defaults)
		assert.NotNil(t, service)
	})
}

func TestAlertService_SubmitAlert(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestAlertService(t, client)
	ctx := context.Background()

	t.Run("creates session with all fields", func(t *testing.T) {
		input := SubmitAlertInput{
			AlertType: "pod-crash",
			Runbook:   "https://runbook.example.com/pod-crash",
			Data:      "Pod nginx-xyz crashed with exit code 137",
			MCP: &models.MCPSelectionConfig{
				Servers: []models.MCPServerSelection{{Name: "kubernetes"}},
			},
			Author: "test@example.com",
		}

		session, err := service.SubmitAlert(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, session)

		assert.NotEmpty(t, session.ID)
		assert.Equal(t, input.Data, session.AlertData)
		assert.Equal(t, input.AlertType, session.AgentType)
		assert.Equal(t, input.AlertType, session.AlertType)
		assert.Equal(t, "k8s-analysis", session.ChainID)
		assert.Equal(t, alertsession.StatusPending, session.Status)
		assert.NotZero(t, session.CreatedAt, "created_at should be set at submission")
		assert.Nil(t, session.StartedAt, "started_at should be nil until worker claims session")
		require.NotNil(t, session.Author)
		assert.Equal(t, input.Author, *session.Author)
		require.NotNil(t, session.RunbookURL)
		assert.Equal(t, input.Runbook, *session.RunbookURL)
	})

	t.Run("creates session with minimal fields and default alert type", func(t *testing.T) {
		input := SubmitAlertInput{
			Data: "Generic alert data",
		}

		session, err := service.SubmitAlert(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, session)

		assert.NotEmpty(t, session.ID)
		assert.Equal(t, input.Data, session.AlertData)
		assert.Equal(t, "generic", session.AgentType) // Should use default
		assert.Equal(t, "generic", session.AlertType)
		assert.Equal(t, "default-chain", session.ChainID)
		assert.Equal(t, alertsession.StatusPending, session.Status)
		assert.Nil(t, session.Author)
		assert.Nil(t, session.RunbookURL)
	})

	t.Run("validates alert data is required", func(t *testing.T) {
		input := SubmitAlertInput{
			AlertType: "pod-crash",
			Data:      "",
		}

		session, err := service.SubmitAlert(ctx, input)
		require.Error(t, err)
		assert.Nil(t, session)

		var validErr *ValidationError
		require.ErrorAs(t, err, &validErr)
		assert.Equal(t, "data", validErr.Field)
		assert.Contains(t, validErr.Error(), "required")
	})

	t.Run("rejects invalid alert type", func(t *testing.T) {
		input := SubmitAlertInput{
			AlertType: "nonexistent-type",
			Data:      "Some data",
		}

		session, err := service.SubmitAlert(ctx, input)
		require.Error(t, err)
		assert.Nil(t, session)

		var validErr *ValidationError
		require.ErrorAs(t, err, &validErr)
		assert.Equal(t, "alert_type", validErr.Field)
		assert.Contains(t, validErr.Error(), "no chain found")
		assert.Contains(t, validErr.Error(), "nonexistent-type")
	})

	t.Run("handles MCP selection", func(t *testing.T) {
		input := SubmitAlertInput{
			Data: "Alert with MCP config",
			MCP: &models.MCPSelectionConfig{
				Servers: []models.MCPServerSelection{
					{Name: "kubernetes"},
					{Name: "aws"},
				},
			},
		}

		session, err := service.SubmitAlert(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, session)

		assert.NotEmpty(t, session.ID)
		require.NotNil(t, session.McpSelection)
		assert.NotEmpty(t, session.McpSelection)
	})

	t.Run("handles empty optional fields", func(t *testing.T) {
		input := SubmitAlertInput{
			Data:      "Alert data",
			AlertType: "pod-crash",
			Author:    "",
			Runbook:   "",
			MCP:       nil,
		}

		session, err := service.SubmitAlert(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, session)

		assert.Nil(t, session.Author)
		assert.Nil(t, session.RunbookURL)
	})
}
