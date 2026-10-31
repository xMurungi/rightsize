package aggregator

import (
	"rightsize/internal/model"
	"time"
)

type Hourly struct {
	Hour    time.Time
	Key     model.Key
	Samples int64
	CPUAvg  float64
	CPUMax  int64
	MemAvg  float64
	MemMax  int64
}

type runningTotals struct {
	count          int64
	cpuSum, memSum float64
	cpuMax, memMax int64
}

type Aggregator struct {
	hour        time.Time
	totalsByKey map[model.Key]*runningTotals
}

func New() *Aggregator {
	return &Aggregator{
		totalsByKey: map[model.Key]*runningTotals{},
	}
}

// Add records one poll taken at `now`. If `now` is in a later hour than the
// one being accumulated, the finished hour is returned.
func (aggregator *Aggregator) Add(now time.Time, usages []model.Usage) []Hourly {

	var finished []Hourly
	hourStart := now.UTC().Truncate(time.Hour)

	if !aggregator.hour.IsZero() && hourStart.After(aggregator.hour) {
		finished = aggregator.Flush()
	}
	if aggregator.hour.IsZero() || hourStart.After(aggregator.hour) {
		aggregator.hour = hourStart
	}

	for _, usage := range usages {
		totals := aggregator.totalsByKey[usage.Key]
		if totals == nil {
			totals = &runningTotals{}
			aggregator.totalsByKey[usage.Key] = totals
		}
		totals.count++
		totals.cpuSum += float64(usage.CPUMilli)
		totals.memSum += float64(usage.MemBytes)
		totals.cpuMax = max(totals.cpuMax, usage.CPUMilli)
		totals.memMax = max(totals.memMax, usage.MemBytes)
	}
	return finished
}

// Flush returns the hour in progress and starts a fresh one.
func (aggregator *Aggregator) Flush() []Hourly {
	out := make([]Hourly, 0, len(aggregator.totalsByKey))
	for k, s := range aggregator.totalsByKey {
		out = append(out, Hourly{
			Hour:    aggregator.hour,
			Key:     k,
			Samples: s.count,
			CPUAvg:  s.cpuSum / float64(s.count), CPUMax: s.cpuMax,
			MemAvg: s.memSum / float64(s.count), MemMax: s.memMax,
		})
	}
	aggregator.totalsByKey = map[model.Key]*runningTotals{}
	return out
}

// Merge combines two summaries of the same container and hour (for example
// after a restart mid-hour). Averages must be weighted by sample count.
func Merge(x, y Hourly) Hourly {
	n := float64(x.Samples + y.Samples)
	return Hourly{
		Hour: x.Hour, Key: x.Key, Samples: x.Samples + y.Samples,
		CPUAvg: (x.CPUAvg*float64(x.Samples) + y.CPUAvg*float64(y.Samples)) / n,
		MemAvg: (x.MemAvg*float64(x.Samples) + y.MemAvg*float64(y.Samples)) / n,
		CPUMax: max(x.CPUMax, y.CPUMax),
		MemMax: max(x.MemMax, y.MemMax),
	}
}
