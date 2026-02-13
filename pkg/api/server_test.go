package api

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
