package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"rightsize/internal/aggregator"
	"rightsize/internal/model"
)

func TestSaveMergesSameHourAndWindowCombinesHours(t *testing.T) {
	ctx := context.Background()
	hourlyStore, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer hourlyStore.Close()

	key := model.Key{Namespace: "payments", Workload: "api", Container: "app"}
	hour10 := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	hour11 := hour10.Add(time.Hour)

	save := func(row aggregator.Hourly) {
		t.Helper()
		if err := hourlyStore.SaveHourly(ctx, []aggregator.Hourly{row}); err != nil {
			t.Fatal(err)
		}
	}
	// The 10:00 hour is saved in two pieces, as after a restart. It must become one row.
	save(aggregator.Hourly{Hour: hour10, Key: key, Samples: 3, MemAvg: 200, MemMax: 300})
	save(aggregator.Hourly{Hour: hour10, Key: key, Samples: 1, MemAvg: 500, MemMax: 500})
	save(aggregator.Hourly{Hour: hour11, Key: key, Samples: 4, MemAvg: 100, MemMax: 150})

	stats, err := hourlyStore.Window(ctx, hour10)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("got %d containers, want 1", len(stats))
	}
	got := stats[0]
	// 10:00 merged = n4 avg 275 max 500. 11:00 = n4 avg 100 max 150.
	// Window avg = (275*4 + 100*4) / 8 = 187.5, window max = 500.
	if got.HoursOfData != 2 {
		t.Errorf("hours = %d, want 2", got.HoursOfData)
	}
	if got.MemAvg != 187.5 {
		t.Errorf("MemAvg = %v, want 187.5", got.MemAvg)
	}
	if got.MemMax != 500 {
		t.Errorf("MemMax = %v, want 500", got.MemMax)
	}
}

func TestPruneDeletesOnlyOlderHours(t *testing.T) {
	ctx := context.Background()
	hourlyStore, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer hourlyStore.Close()

	key := model.Key{Namespace: "payments", Workload: "api", Container: "app"}
	hour09 := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	hour10 := hour09.Add(time.Hour)
	hour11 := hour10.Add(time.Hour)

	var rows []aggregator.Hourly

	for _, hour := range []time.Time{hour09, hour10, hour11} {
		rows = append(rows, aggregator.Hourly{Hour: hour, Key: key, Samples: 1, MemAvg: 100, MemMax: 100})
	}

	if err := hourlyStore.SaveHourly(ctx, rows); err != nil {
		t.Fatal(err)
	}

	if err := hourlyStore.Prune(ctx, hour10); err != nil {
		t.Fatal(err)
	}

	stats, err := hourlyStore.Window(ctx, hour09)
	if err != nil {
		t.Fatal(err)
	}
	if len(stats) != 1 {
		t.Fatalf("got %d containers, want 1", len(stats))
	}
	if stats[0].HoursOfData != 2 {
		t.Errorf("hours left = %d, want 2 (10:00 and 11:00)", stats[0].HoursOfData)
	}
}

func TestSaveSpecsLatestWinsButOOMOnlyMovesForward(t *testing.T) {
	ctx := context.Background()
	hourlyStore, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer hourlyStore.Close()

	key := model.Key{Namespace: "payments", Workload: "api", Container: "app"}
	oomTime := time.Date(2026, 10, 30, 9, 30, 0, 0, time.UTC)
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)

	first := model.Spec{Key: key, MemRequestBytes: 1000, LastOOM: oomTime}
	if err := hourlyStore.SaveSpecs(ctx, []model.Spec{first}, now); err != nil {
		t.Fatal(err)
	}
	// Later: someone lowered the request, and the fresh pod has no OOM in its status.
	second := model.Spec{Key: key, MemRequestBytes: 500}
	if err := hourlyStore.SaveSpecs(ctx, []model.Spec{second}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	specs, err := hourlyStore.Specs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 {
		t.Fatalf("got %d specs, want 1", len(specs))
	}
	if specs[0].MemRequestBytes != 500 {
		t.Errorf("request = %d, want 500 (latest wins)", specs[0].MemRequestBytes)
	}
	if !specs[0].LastOOM.Equal(oomTime) {
		t.Errorf("last OOM = %v, want %v (must not be forgotten)", specs[0].LastOOM, oomTime)
	}
}

func TestPruneDeletesStaleSpecs(t *testing.T) {
	ctx := context.Background()
	hourlyStore, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer hourlyStore.Close()

	deletedKey := model.Key{Namespace: "ns", Workload: "deleted-app", Container: "app"}
	liveKey := model.Key{Namespace: "ns", Workload: "live-app", Container: "app"}
	cutoff := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)

	// The deleted app was last seen before the cutoff; the live one after it.
	if err := hourlyStore.SaveSpecs(ctx, []model.Spec{{Key: deletedKey, MemRequestBytes: 1000}}, cutoff.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := hourlyStore.SaveSpecs(ctx, []model.Spec{{Key: liveKey, MemRequestBytes: 1000}}, cutoff.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	if err := hourlyStore.Prune(ctx, cutoff); err != nil {
		t.Fatal(err)
	}

	specs, err := hourlyStore.Specs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].Key != liveKey {
		t.Fatalf("specs after prune = %+v, want only the live app", specs)
	}
}
