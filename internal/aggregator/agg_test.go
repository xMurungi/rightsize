package aggregator

import "testing"

func TestMergeWeightsByCount(t *testing.T) {
	a := Hourly{Samples: 3, MemAvg: 200, MemMax: 300}
	b := Hourly{Samples: 1, MemAvg: 500, MemMax: 500}
	got := Merge(a, b)
	// (200*3 + 500*1) / 4 = 275. A plain average of averages would give 350, which is wrong.
	if got.MemAvg != 275 {
		t.Errorf("MemAvg = %v, want 275", got.MemAvg)
	}
	if got.MemMax != 500 {
		t.Errorf("MemMax = %v, want 500", got.MemMax)
	}
}
