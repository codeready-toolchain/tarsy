package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewNotifyListener(t *testing.T) {
	manager := NewConnectionManager(&mockCatchupQuerier{}, 0)
	listener := NewNotifyListener("host=localhost dbname=test", manager)

	assert.NotNil(t, listener)
	assert.Equal(t, "host=localhost dbname=test", listener.connString)
	assert.NotNil(t, listener.channels)
	assert.Equal(t, manager, listener.manager)
}

func TestNotifyListener_ChannelTrackingWithoutConnection(t *testing.T) {
	// Without calling Start(), the listener has no connection.
	// Subscribe/Unsubscribe should return errors gracefully.
	manager := NewConnectionManager(&mockCatchupQuerier{}, 0)
	listener := NewNotifyListener("host=localhost dbname=test", manager)

	t.Run("subscribe without connection returns error", func(t *testing.T) {
		err := listener.Subscribe(t.Context(), "test-channel")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not established")
	})

	t.Run("unsubscribe without connection is a no-op", func(t *testing.T) {
		err := listener.Unsubscribe(t.Context(), "test-channel")
		assert.NoError(t, err) // Not listening, so no-op
	})
}
