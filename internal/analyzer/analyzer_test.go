package analyzer

import (
	"testing"
	"time"

	"rightsize/internal/model"
	"rightsize/internal/store"
)

const mebibyte = int64(1) << 20

func TestClassify(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	config := Config{Margin: 0.2, MinHours: 24, OOMWindow: 7 * 24 * time.Hour, WasteWarnPct: 30}

	tests := []struct {
		name               string
		spec               model.Spec
		stat               store.WindowStat
		wantStatus         string
		wantMemWaste       int64
		wantMemRecommended int64
	}{
		{
			name:               "heavily over-booked",
			spec:               model.Spec{MemRequestBytes: 2048 * mebibyte, CPURequestMilli: 1000},
			stat:               store.WindowStat{HoursOfData: 168, MemMax: 500 * mebibyte, CPUMax: 150},
			wantStatus:         StatusOver,
			wantMemWaste:       1548 * mebibyte,
			wantMemRecommended: 600 * mebibyte,
		},
		{
			name:               "about right",
			spec:               model.Spec{MemRequestBytes: 1000 * mebibyte, CPURequestMilli: 500},
			stat:               store.WindowStat{HoursOfData: 168, MemMax: 900 * mebibyte, CPUMax: 450},
			wantStatus:         StatusOK,
			wantMemWaste:       100 * mebibyte,
			wantMemRecommended: 1080 * mebibyte,
		},
		{
			name:               "uses more memory than booked",
			spec:               model.Spec{MemRequestBytes: 512 * mebibyte, CPURequestMilli: 250},
			stat:               store.WindowStat{HoursOfData: 168, MemMax: 620 * mebibyte, CPUMax: 200},
			wantStatus:         StatusUnder,
			wantMemWaste:       0,
			wantMemRecommended: 744 * mebibyte,
		},
		{
			name:               "recent OOM blocks shrinking, even with little data",
			spec:               model.Spec{MemRequestBytes: 2048 * mebibyte, LastOOM: now.Add(-3 * time.Hour)},
			stat:               store.WindowStat{HoursOfData: 5, MemMax: 300 * mebibyte},
			wantStatus:         StatusDoNotShrink,
			wantMemWaste:       1748 * mebibyte,
			wantMemRecommended: 2048 * mebibyte,
		},
		{
			name:               "old OOM outside the window is ignored",
			spec:               model.Spec{MemRequestBytes: 2048 * mebibyte, CPURequestMilli: 100, LastOOM: now.Add(-30 * 24 * time.Hour)},
			stat:               store.WindowStat{HoursOfData: 168, MemMax: 300 * mebibyte, CPUMax: 90},
			wantStatus:         StatusOver,
			wantMemWaste:       1748 * mebibyte,
			wantMemRecommended: 360 * mebibyte,
		},
		{
			name:               "not enough data yet",
			spec:               model.Spec{MemRequestBytes: 2048 * mebibyte},
			stat:               store.WindowStat{HoursOfData: 6, MemMax: 300 * mebibyte},
			wantStatus:         StatusInsufficientData,
			wantMemWaste:       1748 * mebibyte,
			wantMemRecommended: 0,
		},
		{
			name:               "NO data at all must not look like 100% waste",
			spec:               model.Spec{MemRequestBytes: 2048 * mebibyte},
			stat:               store.WindowStat{}, // zero value: nothing was ever recorded
			wantStatus:         StatusInsufficientData,
			wantMemWaste:       0,
			wantMemRecommended: 0,
		},
		{
			name:               "nothing requested at all",
			spec:               model.Spec{},
			stat:               store.WindowStat{HoursOfData: 168, MemMax: 60 * mebibyte, CPUMax: 25},
			wantStatus:         StatusNoRequest,
			wantMemWaste:       0,
			wantMemRecommended: 72 * mebibyte,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Classify(test.spec, test.stat, config, now)
			if got.Status != test.wantStatus {
				t.Errorf("status = %q, want %q", got.Status, test.wantStatus)
			}
			if got.MemWasteBytes != test.wantMemWaste {
				t.Errorf("memory waste = %d, want %d", got.MemWasteBytes, test.wantMemWaste)
			}
			if got.MemRecommendedBytes != test.wantMemRecommended {
				t.Errorf("memory recommended = %d, want %d", got.MemRecommendedBytes, test.wantMemRecommended)
			}
		})
	}
}
