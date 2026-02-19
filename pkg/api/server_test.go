package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/codeready-toolchain/tarsy/pkg/config"
	"github.com/codeready-toolchain/tarsy/pkg/events"
	"github.com/codeready-toolchain/tarsy/pkg/queue"
	"github.com/codeready-toolchain/tarsy/pkg/services"
)

func TestServer_ValidateWiring(t *testing.T) {
	t.Run("all services wired", func(t *testing.T) {
		s := &Server{
			chatService:        &services.ChatService{},
			chatExecutor:       &queue.ChatMessageExecutor{},
			eventPublisher:     events.NewEventPublisher(nil),
			interactionService: &services.InteractionService{},
			stageService:       &services.StageService{},
			timelineService:    &services.TimelineService{},
		}
		assert.NoError(t, s.ValidateWiring())
	})

	t.Run("no services wired", func(t *testing.T) {
		s := &Server{}
		err := s.ValidateWiring()
		require.Error(t, err)

		msg := err.Error()
		assert.Contains(t, msg, "server wiring incomplete")
		assert.Contains(t, msg, "chatService")
		assert.Contains(t, msg, "chatExecutor")
		assert.Contains(t, msg, "eventPublisher")
		assert.Contains(t, msg, "interactionService")
		assert.Contains(t, msg, "stageService")
		assert.Contains(t, msg, "timelineService")

		// All 6 services should be reported.
		assert.Equal(t, 6, strings.Count(msg, "not set"))
	})

	t.Run("partial wiring reports only missing", func(t *testing.T) {
		s := &Server{
			chatService:    &services.ChatService{},
			chatExecutor:   &queue.ChatMessageExecutor{},
			eventPublisher: events.NewEventPublisher(nil),
			// interactionService, stageService, timelineService intentionally omitted
		}
		err := s.ValidateWiring()
		require.Error(t, err)

		msg := err.Error()
		assert.Contains(t, msg, "interactionService")
		assert.Contains(t, msg, "stageService")
		assert.Contains(t, msg, "timelineService")
		assert.NotContains(t, msg, "chatService")
		assert.NotContains(t, msg, "chatExecutor")
		assert.NotContains(t, msg, "eventPublisher")
	})

	t.Run("optional services not checked", func(t *testing.T) {
		// healthMonitor and warningService are legitimately optional
		// (MCP-gated). ValidateWiring should pass without them.
		s := &Server{
			chatService:        &services.ChatService{},
			chatExecutor:       &queue.ChatMessageExecutor{},
			eventPublisher:     events.NewEventPublisher(nil),
			interactionService: &services.InteractionService{},
			stageService:       &services.StageService{},
			timelineService:    &services.TimelineService{},
			// healthMonitor and warningService intentionally nil
		}
		assert.NoError(t, s.ValidateWiring())
	})
}

func TestParseDashboardOrigin(t *testing.T) {
	tests := []struct {
		name       string
		raw        string
		wantOrigin string
		wantHost   string
		wantOK     bool
	}{
		{
			name:       "full URL with scheme",
			raw:        "https://tarsy.example.com",
			wantOrigin: "https://tarsy.example.com",
			wantHost:   "tarsy.example.com",
			wantOK:     true,
		},
		{
			name:       "URL with port",
			raw:        "http://localhost:5173",
			wantOrigin: "http://localhost:5173",
			wantHost:   "localhost:5173",
			wantOK:     true,
		},
		{
			name:       "URL with path stripped",
			raw:        "http://example.com:8080/dashboard",
			wantOrigin: "http://example.com:8080",
			wantHost:   "example.com:8080",
			wantOK:     true,
		},
		{
			name:       "no scheme defaults to http",
			raw:        "example.com:8080",
			wantOrigin: "http://example.com:8080",
			wantHost:   "example.com:8080",
			wantOK:     true,
		},
		{
			name:   "empty string",
			raw:    "",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			origin, host, ok := parseDashboardOrigin(tt.raw)
			assert.Equal(t, tt.wantOK, ok)
			if ok {
				assert.Equal(t, tt.wantOrigin, origin)
				assert.Equal(t, tt.wantHost, host)
			}
		})
	}
}

func TestServer_resolveWSOriginPatterns(t *testing.T) {
	tests := []struct {
		name             string
		dashboardURL     string
		allowedWSOrigins []string
		wantContains     []string
		wantLen          int
	}{
		{
			name:         "dashboard URL parsed to host",
			dashboardURL: "https://tarsy.example.com",
			wantContains: []string{"tarsy.example.com", "localhost:*", "127.0.0.1:*"},
			wantLen:      3,
		},
		{
			name:         "dashboard URL with port",
			dashboardURL: "http://localhost:5173",
			wantContains: []string{"localhost:5173", "localhost:*", "127.0.0.1:*"},
			wantLen:      3,
		},
		{
			name:         "empty dashboard URL still includes localhost",
			dashboardURL: "",
			wantContains: []string{"localhost:*", "127.0.0.1:*"},
			wantLen:      2,
		},
		{
			name:             "additional origins appended",
			dashboardURL:     "https://tarsy.example.com",
			allowedWSOrigins: []string{"*.internal.corp:*"},
			wantContains:     []string{"tarsy.example.com", "localhost:*", "127.0.0.1:*", "*.internal.corp:*"},
			wantLen:          4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Server{
				cfg: &config.Config{
					DashboardURL:     tt.dashboardURL,
					AllowedWSOrigins: tt.allowedWSOrigins,
				},
			}
			patterns := s.resolveWSOriginPatterns()
			assert.Len(t, patterns, tt.wantLen)
			for _, want := range tt.wantContains {
				assert.Contains(t, patterns, want)
			}
		})
	}
}
