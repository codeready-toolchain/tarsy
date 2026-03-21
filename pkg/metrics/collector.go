package metrics

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const (
	pollInterval = 15 * time.Second
	pollTimeout  = 10 * time.Second
)

// ReviewCounts holds per-rating session totals.
type ReviewCounts struct {
	Accurate, Partial, Inaccurate int
}

// SessionCounter abstracts the DB queries needed for gauge polling.
type SessionCounter interface {
	PendingCount(ctx context.Context) (int, error)
	ActiveCount(ctx context.Context) (int, error)
	ReviewCountsByRating(ctx context.Context) (ReviewCounts, error)
}

// GaugeCollector periodically polls the database to update global session
// gauges (active / queued) that cannot be maintained event-driven because
// multiple pods share the work.
type GaugeCollector struct {
	counter SessionCounter
	stopFn  context.CancelFunc
	wg      sync.WaitGroup
}

// NewGaugeCollector returns a GaugeCollector that polls the given counter.
func NewGaugeCollector(counter SessionCounter) *GaugeCollector {
	return &GaugeCollector{counter: counter}
}

// Start begins the polling loop. The provided ctx is used as the parent for
// each poll query; call Stop to terminate the loop.
func (g *GaugeCollector) Start(ctx context.Context) {
	pollCtx, cancel := context.WithCancel(ctx)
	g.stopFn = cancel

	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		g.boundedPoll(pollCtx)

		for {
			select {
			case <-pollCtx.Done():
				return
			case <-time.After(pollInterval):
				g.boundedPoll(pollCtx)
			}
		}
	}()
}

// Stop terminates the polling loop and blocks until it exits.
func (g *GaugeCollector) Stop() {
	if g.stopFn != nil {
		g.stopFn()
	}
	g.wg.Wait()
}

func (g *GaugeCollector) boundedPoll(parent context.Context) {
	ctx, cancel := context.WithTimeout(parent, pollTimeout)
	defer cancel()
	g.poll(ctx)
}

func (g *GaugeCollector) poll(ctx context.Context) {
	if n, err := g.counter.ActiveCount(ctx); err != nil {
		slog.Warn("metrics: failed to poll active session count", "error", err)
	} else {
		SessionsActive.Set(float64(n))
	}

	if n, err := g.counter.PendingCount(ctx); err != nil {
		slog.Warn("metrics: failed to poll pending session count", "error", err)
	} else {
		SessionsQueued.Set(float64(n))
	}

	if rc, err := g.counter.ReviewCountsByRating(ctx); err != nil {
		slog.Warn("metrics: failed to poll review counts", "error", err)
	} else {
		SessionsReviewedTotal.WithLabelValues("accurate").Set(float64(rc.Accurate))
		SessionsReviewedTotal.WithLabelValues("partially_accurate").Set(float64(rc.Partial))
		SessionsReviewedTotal.WithLabelValues("inaccurate").Set(float64(rc.Inaccurate))
	}
}
