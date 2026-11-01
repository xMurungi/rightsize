package collector

import (
	"context"
	"log"
	"rightsize/internal/aggregator"
	"rightsize/internal/source"
	"rightsize/internal/store"
	"time"
)

type Collector struct {
	source     source.Source
	aggregator *aggregator.Aggregator
	store      *store.Store
	interval   time.Duration
	now        func() time.Time    // a field so tests can fake the clock
	pending    []aggregator.Hourly // finished hours not yet safely saved
}

func New(src source.Source, hourlyStore *store.Store, interval time.Duration) *Collector {
	return &Collector{
		source:     src,
		aggregator: aggregator.New(),
		store:      hourlyStore,
		interval:   interval,
		now:        func() time.Time { return time.Now().UTC() },
	}
}

// Run blocks until ctx is cancelled, then saves the partial hour and returns.
func (collector *Collector) Run(ctx context.Context) {

	ticker := time.NewTicker(collector.interval)
	defer ticker.Stop()

	collector.poll(ctx) // first reading now, not one interval from now
	for {
		select {
		case <-ctx.Done():
			collector.shutdown()
			return
		case <-ticker.C:
			collector.poll(ctx)
		}
	}
}

func (collector *Collector) poll(ctx context.Context) {
	usages, err := collector.source.Usage(ctx)
	if err != nil {
		log.Printf("collector: read usage: %v", err)
		return // skip this tick; one bad poll must not stop the collector
	}

	finished := collector.aggregator.Add(collector.now(), usages)
	collector.pending = append(collector.pending, finished...)
	collector.savePending(ctx)
}

func (collector *Collector) savePending(ctx context.Context) {
	if len(collector.pending) == 0 {
		return
	}

	if err := collector.store.SaveHourly(ctx, collector.pending); err != nil {
		log.Printf("collector: save %d rows, will retry: %v", len(collector.pending), err)
		return // keep pending; the next poll retries
	}

	collector.pending = nil
}

func (collector *Collector) shutdown() {
	// ctx is already cancelled, so a save using it would fail instantly.
	// Use a fresh context with a deadline shorter than Kubernetes' grace period.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	collector.pending = append(collector.pending, collector.aggregator.Flush()...)
	collector.savePending(shutdownCtx)
}
