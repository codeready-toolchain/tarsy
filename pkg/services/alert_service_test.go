package services

import (
	"context"
	"testing"

	"github.com/codeready-toolchain/tarsy/ent/alertsession"
	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/database"
	"github.com/codeready-toolchain/tarsy/pkg/masking"
	"github.com/codeready-toolchain/tarsy/pkg/models"
	testdb "github.com/codeready-toolchain/tarsy/test/database"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestAlertService(t *testing.T, client *database.Client, maskingSvc ...*masking.Service) *AlertService {
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

	var svc *masking.Service
	if len(maskingSvc) > 0 {
		svc = maskingSvc[0]
	}

	return NewAlertService(client.Client, chainRegistry, defaults, svc)
}

func TestNewAlertService(t *testing.T) {
	client := testdb.NewTestClient(t)
	chainRegistry := config.NewChainRegistry(map[string]*config.ChainConfig{})
	defaults := &config.Defaults{AlertType: "generic"}

	t.Run("panics when chainRegistry is nil", func(t *testing.T) {
		assert.Panics(t, func() {
			NewAlertService(client.Client, nil, defaults, nil)
		})
	})

	t.Run("panics when defaults is nil", func(t *testing.T) {
		assert.Panics(t, func() {
			NewAlertService(client.Client, chainRegistry, nil, nil)
		})
	})

	t.Run("succeeds with valid inputs", func(t *testing.T) {
		service := NewAlertService(client.Client, chainRegistry, defaults, nil)
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

	t.Run("stores slack message fingerprint", func(t *testing.T) {
		input := SubmitAlertInput{
			Data:                    "Alert with fingerprint",
			AlertType:               "pod-crash",
			SlackMessageFingerprint: "Pod nginx crashed OOMKilled",
		}

		session, err := service.SubmitAlert(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, session)

		stored, err := client.AlertSession.Get(ctx, session.ID)
		require.NoError(t, err)
		require.NotNil(t, stored.SlackMessageFingerprint)
		assert.Equal(t, "Pod nginx crashed OOMKilled", *stored.SlackMessageFingerprint)
	})

	t.Run("omits slack message fingerprint when empty", func(t *testing.T) {
		input := SubmitAlertInput{
			Data:      "Alert without fingerprint",
			AlertType: "pod-crash",
		}

		session, err := service.SubmitAlert(ctx, input)
		require.NoError(t, err)
		require.NotNil(t, session)

		stored, err := client.AlertSession.Get(ctx, session.ID)
		require.NoError(t, err)
		assert.Nil(t, stored.SlackMessageFingerprint)
	})
}

// --- Alert masking tests ---

func TestAlertService_SubmitAlert_MaskingApplied(t *testing.T) {
	client := testdb.NewTestClient(t)
	maskingSvc := masking.NewService(
		config.NewMCPServerRegistry(nil),
		masking.AlertMaskingConfig{Enabled: true, PatternGroup: "security"},
	)
	service := setupTestAlertService(t, client, maskingSvc)
	ctx := context.Background()

	input := SubmitAlertInput{
		Data: `Alert: password: "FAKE-S3CRET-NOT-REAL" found in config. Contact user@example.com`,
	}

	session, err := service.SubmitAlert(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, session)

	// Read back from DB to verify masking was applied before storage
	stored, err := client.AlertSession.Get(ctx, session.ID)
	require.NoError(t, err)

	assert.NotContains(t, stored.AlertData, "FAKE-S3CRET-NOT-REAL", "Password should be masked")
	assert.NotContains(t, stored.AlertData, "user@example.com", "Email should be masked")
	assert.Contains(t, stored.AlertData, "[MASKED_PASSWORD]")
	assert.Contains(t, stored.AlertData, "[MASKED_EMAIL]")
}

func TestAlertService_SubmitAlert_MaskingDisabled(t *testing.T) {
	client := testdb.NewTestClient(t)
	maskingSvc := masking.NewService(
		config.NewMCPServerRegistry(nil),
		masking.AlertMaskingConfig{Enabled: false, PatternGroup: "security"},
	)
	service := setupTestAlertService(t, client, maskingSvc)
	ctx := context.Background()

	input := SubmitAlertInput{
		Data: `password: "FAKE-S3CRET-NOT-REAL"`,
	}

	session, err := service.SubmitAlert(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, session)

	stored, err := client.AlertSession.Get(ctx, session.ID)
	require.NoError(t, err)

	assert.Equal(t, input.Data, stored.AlertData, "Data should be stored as-is when masking disabled")
}

func TestAlertService_SubmitAlert_NilService(t *testing.T) {
	client := testdb.NewTestClient(t)
	service := setupTestAlertService(t, client, nil)
	ctx := context.Background()

	input := SubmitAlertInput{
		Data: `password: "FAKE-S3CRET-NOT-REAL"`,
	}

	session, err := service.SubmitAlert(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, session)

	stored, err := client.AlertSession.Get(ctx, session.ID)
	require.NoError(t, err)

	assert.Equal(t, input.Data, stored.AlertData, "Data should be stored as-is with nil masking service")
}
