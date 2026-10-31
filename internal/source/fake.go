package source

import (
	"context"
	"math/rand"
	"rightsize/internal/model"
)

type Fake struct {
	rng *rand.Rand // *rand.Rand is NOT safe for concurrent use; fine for now, remember it for the collector
}

// jitter returns base ±10%.
func (f *Fake) jitter(base int64) int64 {
	return int64(float64(base) * (0.9 + 0.2*f.rng.Float64()))
}

func NewFake(seed int64) *Fake {
	return &Fake{rng: rand.New(rand.NewSource(seed))}
}

func (f *Fake) Usage(ctx context.Context) ([]model.Usage, error) {
	return []model.Usage{
		{
			Key:      model.Key{Namespace: "Payments", Workload: "Payments-api", Container: "app"},
			CPUMilli: f.jitter(120),
			MemBytes: f.jitter(450 << 20), // 450MiB
		},
		{
			Key:      model.Key{Namespace: "dukamarketplace", Workload: "slackbot", Container: "app"},
			CPUMilli: f.jitter(40),
			MemBytes: f.jitter(180 << 20),
		},
	}, nil
}
