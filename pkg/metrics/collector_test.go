package metrics

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stubCounter struct {
	active, pending int
	activeErr       error
	pendingErr      error
}

func (s *stubCounter) ActiveCount(context.Context) (int, error) {
	return s.active, s.activeErr
}

func (s *stubCounter) PendingCount(context.Context) (int, error) {
	return s.pending, s.pendingErr
}

func TestGaugeCollector_Poll(t *testing.T) {
	t.Run("sets gauges from counter", func(t *testing.T) {
		sc := &stubCounter{active: 3, pending: 7}
		gc := NewGaugeCollector(sc)

		gc.poll(t.Context())

		assert.Equal(t, float64(3), testutil.ToFloat64(SessionsActive))
		assert.Equal(t, float64(7), testutil.ToFloat64(SessionsQueued))
	})

	t.Run("active error leaves gauge unchanged", func(t *testing.T) {
		SessionsActive.Set(99)
		sc := &stubCounter{activeErr: fmt.Errorf("db down"), pending: 5}
		gc := NewGaugeCollector(sc)

		gc.poll(t.Context())

		assert.Equal(t, float64(99), testutil.ToFloat64(SessionsActive))
		assert.Equal(t, float64(5), testutil.ToFloat64(SessionsQueued))
	})

	t.Run("pending error leaves gauge unchanged", func(t *testing.T) {
		SessionsQueued.Set(42)
		sc := &stubCounter{active: 2, pendingErr: fmt.Errorf("db down")}
		gc := NewGaugeCollector(sc)

		gc.poll(t.Context())

		assert.Equal(t, float64(2), testutil.ToFloat64(SessionsActive))
		assert.Equal(t, float64(42), testutil.ToFloat64(SessionsQueued))
	})
}

func TestGaugeCollector_StartStop(t *testing.T) {
	sc := &stubCounter{active: 1, pending: 2}
	gc := NewGaugeCollector(sc)

	gc.Start(t.Context())

	// The first poll happens synchronously inside the goroutine before the
	// ticker loop, so the gauges should be set almost immediately.
	require.Eventually(t, func() bool {
		return testutil.ToFloat64(SessionsActive) == 1 &&
			testutil.ToFloat64(SessionsQueued) == 2
	}, time.Second, 10*time.Millisecond)

	gc.Stop()
}

func TestGaugeCollector_StopIdempotent(t *testing.T) {
	gc := NewGaugeCollector(&stubCounter{})
	gc.Start(t.Context())
	gc.Stop()
	assert.NotPanics(t, func() { gc.Stop() })
}
