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
