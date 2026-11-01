package main

import (
	"context"
	"fmt"
	"time"

	"rightsize/internal/aggregator"
	"rightsize/internal/source"
)

func main() {
	src := source.NewFake(1)
	ag := aggregator.New()
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 130; i++ { // 130 simulated minutes, so it crosses two hour boundaries
		us, err := src.Snapshot(context.Background())
		if err != nil {
			panic(err)
		}
		for _, h := range ag.Add(now, us.Usages) {
			fmt.Printf("%s %-14s n=%d memAvg=%.0fMi memMax=%dMi\n",
				h.Hour.Format("15:04"), h.Key.Workload, h.Samples, h.MemAvg/(1<<20), h.MemMax>>20)
		}
		now = now.Add(time.Minute)
	}
}
