package collector

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"rightsize/internal/model"
	"rightsize/internal/store"
)

type scriptedSource struct {
	usages []model.Usage
}

func (scripted *scriptedSource) Usage(ctx context.Context) ([]model.Usage, error) {
	return scripted.usages, nil
}

func TestHourRolloverSavesFinishedHour(t *testing.T) {
	ctx := context.Background()
	hourlyStore, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer hourlyStore.Close()

	key := model.Key{Namespace: "payments", Workload: "api", Container: "app"}
	src := &scriptedSource{usages: []model.Usage{{Key: key, MemBytes: 100}}}

	clock := time.Date(2026, 10, 30, 10, 59, 0, 0, time.UTC)
	collector := New(src, hourlyStore, time.Minute)
	collector.now = func() time.Time { return clock }
	since := clock.Add(-24 * time.Hour)

	collector.poll(ctx) // 10:59, still inside the 10:00 hour
	stats, err := hourlyStore.Window(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 0 {
		t.Fatalf("saved %d containers mid-hour, want 0", len(stats))
	}

	clock = clock.Add(time.Minute) // 11:00, the 10:00 hour is now finished
	collector.poll(ctx)
	stats, err = hourlyStore.Window(ctx, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 || stats[0].HoursOfData != 1 {
		t.Fatalf("after rollover got %+v, want 1 container with 1 hour", stats)
	}
}
