package slack

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestService_NilReceiver(t *testing.T) {
	var s *Service

	t.Run("NotifySessionStarted is no-op", func(t *testing.T) {
		result := s.NotifySessionStarted(context.Background(), SessionStartedInput{
			SessionID:               "sess-1",
			SlackMessageFingerprint: "test fingerprint",
		})
		assert.Empty(t, result)
	})

	t.Run("NotifySessionCompleted is no-op", func(_ *testing.T) {
		// Should not panic
		s.NotifySessionCompleted(context.Background(), SessionCompletedInput{
			SessionID: "sess-1",
			Status:    "completed",
		})
	})
}

func TestNewService(t *testing.T) {
	t.Run("returns nil when token empty", func(t *testing.T) {
		svc := NewService(ServiceConfig{Token: "", Channel: "C123"})
		assert.Nil(t, svc)
	})

	t.Run("returns nil when channel empty", func(t *testing.T) {
		svc := NewService(ServiceConfig{Token: "xoxb-test", Channel: ""})
		assert.Nil(t, svc)
	})

	t.Run("returns service when configured", func(t *testing.T) {
		svc := NewService(ServiceConfig{
			Token:        "xoxb-test",
			Channel:      "C123",
			DashboardURL: "https://example.com",
		})
		assert.NotNil(t, svc)
	})
}

func TestService_NotifySessionStarted_NoFingerprint(t *testing.T) {
	svc := NewService(ServiceConfig{
		Token:        "xoxb-test",
		Channel:      "C123",
		DashboardURL: "https://example.com",
	})

	result := svc.NotifySessionStarted(context.Background(), SessionStartedInput{
		SessionID:               "sess-1",
		SlackMessageFingerprint: "",
	})
	assert.Empty(t, result, "should skip when no fingerprint")
}
