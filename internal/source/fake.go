package source

import (
	"context"
	"math/rand"
	"time"

	"rightsize/internal/model"
)

const (
	mebibyte = int64(1) << 20
	gibibyte = int64(1) << 30
)

type fakeWorkload struct {
	key             model.Key
	cpuRequestMilli int64
	memRequestBytes int64
	cpuTypicalMilli int64
	memTypicalBytes int64
	oomKilled       bool
}

var fakeWorkloads = []fakeWorkload{
	{ // booked 2 GiB, uses ~450 MiB: way over-booked
		key:             model.Key{Namespace: "payments", Workload: "payments-api", Container: "app"},
		cpuRequestMilli: 1000, memRequestBytes: 2 * gibibyte,
		cpuTypicalMilli: 120, memTypicalBytes: 450 * mebibyte,
	},
	{ // booked 1 GiB, uses ~700 MiB: about right
		key:             model.Key{Namespace: "payments", Workload: "ledger", Container: "app"},
		cpuRequestMilli: 500, memRequestBytes: 1 * gibibyte,
		cpuTypicalMilli: 300, memTypicalBytes: 700 * mebibyte,
	},
	{ // over-booked
		key:             model.Key{Namespace: "dukamarketplace", Workload: "slackbot", Container: "app"},
		cpuRequestMilli: 500, memRequestBytes: 1 * gibibyte,
		cpuTypicalMilli: 40, memTypicalBytes: 180 * mebibyte,
	},
	{ // uses MORE than it booked, and was OOMKilled
		key:             model.Key{Namespace: "dukamarketplace", Workload: "knowledge-processor", Container: "app"},
		cpuRequestMilli: 250, memRequestBytes: 512 * mebibyte,
		cpuTypicalMilli: 200, memTypicalBytes: 620 * mebibyte,
		oomKilled: true,
	},
	{ // booked nothing at all
		key:             model.Key{Namespace: "kube-tools", Workload: "no-requests", Container: "app"},
		cpuTypicalMilli: 20, memTypicalBytes: 60 * mebibyte,
	},
}

type Fake struct {
	rng *rand.Rand
}

func NewFake(seed int64) *Fake {
	return &Fake{rng: rand.New(rand.NewSource(seed))}
}

func (fake *Fake) Snapshot(ctx context.Context) (model.Snapshot, error) {

	var snapshot model.Snapshot

	for _, workload := range fakeWorkloads {
		snapshot.Usages = append(snapshot.Usages, model.Usage{
			Key:      workload.key,
			CPUMilli: fake.jitter(workload.cpuTypicalMilli),
			MemBytes: fake.jitter(workload.memTypicalBytes),
		})

		spec := model.Spec{
			Key:             workload.key,
			CPURequestMilli: workload.cpuRequestMilli,
			MemRequestBytes: workload.memRequestBytes,
			CPULimitMilli:   workload.cpuRequestMilli * 2,
			MemLimitBytes:   workload.memRequestBytes,
		}
		if workload.oomKilled {
			spec.Restarts = 3
			spec.LastOOM = time.Now().Add(-2 * time.Hour)
		}
		snapshot.Specs = append(snapshot.Specs, spec)
	}

	return snapshot, nil
}

func (fake *Fake) jitter(base int64) int64 {
	return int64(float64(base) * (0.9 + 0.2*fake.rng.Float64()))
}
