package analyzer

import (
	"math"
	"rightsize/internal/model"
	"rightsize/internal/store"
	"time"
)

const (
	StatusOK               = "ok"
	StatusOver             = "over-provisioned"
	StatusUnder            = "under-provisioned"
	StatusDoNotShrink      = "do-not-shrink"
	StatusInsufficientData = "insufficient-data"
	StatusNoRequest        = "no-request"
)

type Config struct {
	Margin       float64       // headroom added to max usage, 0.2 = +20%
	MinHours     int           // hours of data needed before a result is trusted
	OOMWindow    time.Duration // an OOMKill this recent blocks shrinking
	WasteWarnPct float64       // waste at or above this % means over-provisioned
}

type Row struct {
	Key         model.Key
	Status      string
	HoursOfData int

	MemRequestBytes     int64
	MemMaxBytes         int64
	MemWasteBytes       int64
	MemWastePct         float64
	MemRecommendedBytes int64

	CPURequestMilli     int64
	CPUMaxMilli         int64
	CPUWasteMilli       int64
	CPUWastePct         float64
	CPURecommendedMilli int64
}

// waste        = max(0, request - max used), only if we have any data (HoursOfData > 0)
// waste %      = waste / request * 100, and 0 if request is 0
// recommended  = max used * (1 + margin), rounded UP to 1 MiB (memory) / 10 millicores (CPU)

// 1. OOMKilled within OOMWindow        -> do-not-shrink      (recommended = current request)
// 2. HoursOfData < MinHours            -> insufficient-data  (no recommendation)
// 3. no memory AND no CPU request      -> no-request         (recommended as above)
// 4. memory max > memory request       -> under-provisioned  (memory only: CPU bursting is normal)
// 5. memory or CPU waste % >= warn     -> over-provisioned
// 6. otherwise                         -> ok

// Classify compares what a container booked with what it used over the window.
func Classify(spec model.Spec, stat store.WindowStat, config Config, now time.Time) Row {

	const (
		cpuStepMilli    = 10
		memoryStepBytes = 1 << 20 // 1 MiB
	)

	row := Row{
		Key:             spec.Key,
		HoursOfData:     stat.HoursOfData,
		MemRequestBytes: spec.MemRequestBytes,
		MemMaxBytes:     stat.MemMax,
		CPURequestMilli: spec.CPURequestMilli,
		CPUMaxMilli:     stat.CPUMax,
	}

	// Step A: numbers. Only if stat.HoursOfData > 0:
	//   waste = max(0, request - max used), then waste %, using the helpers below.
	if stat.HoursOfData > 0 {
		waste := max(0, spec.CPURequestMilli-stat.CPUMax)
		cpuwastepct := wastePercent(waste, spec.CPURequestMilli)
		row.CPUWasteMilli = waste
		row.CPUWastePct = cpuwastepct

		wasteMem := max(0, spec.MemRequestBytes-stat.MemMax)
		memwastepct := wastePercent(wasteMem, spec.MemRequestBytes)
		row.MemWasteBytes = wasteMem
		row.MemWastePct = memwastepct
	}
	// Step B: recommendations = max used * (1 + config.Margin), rounded up.
	row.CPURecommendedMilli = recommend(stat.CPUMax, config.Margin, cpuStepMilli)

	row.MemRecommendedBytes = recommend(stat.MemMax, config.Margin, memoryStepBytes)
	// Step C: status, in this order, return as soon as one matches:
	//   do-not-shrink, insufficient-data, no-request, under-provisioned, over-provisioned, ok
	switch {
	case !spec.LastOOM.IsZero() && now.Sub(spec.LastOOM) <= config.OOMWindow:
		row.Status = StatusDoNotShrink
		row.CPURecommendedMilli = spec.CPURequestMilli // leave it alone: a human should look
		row.MemRecommendedBytes = spec.MemRequestBytes
	case stat.HoursOfData < config.MinHours:
		row.Status = StatusInsufficientData
		row.CPURecommendedMilli = 0 // not enough data to recommend anything
		row.MemRecommendedBytes = 0
	case spec.CPURequestMilli == 0 && spec.MemRequestBytes == 0:
		row.Status = StatusNoRequest
	case stat.MemMax > spec.MemRequestBytes:
		row.Status = StatusUnder
	case row.CPUWastePct >= config.WasteWarnPct || row.MemWastePct >= config.WasteWarnPct:
		row.Status = StatusOver
	default:
		row.Status = StatusOK
	}
	return row
}

// wastePercent: waste as a % of what was requested; 0 if nothing was requested.
func wastePercent(waste, request int64) float64 {
	if request == 0 {
		return 0
	}

	return float64(waste) / float64(request) * 100
}

// recommend: maxUsed * (1+margin), rounded UP to a multiple of step.
func recommend(maxUsed int64, margin float64, step int64) int64 {
	val := float64(maxUsed) * (1 + margin)
	return int64(math.Ceil(val/float64(step))) * step
}

func ClassifyMe(spec model.Spec, stat store.WindowStat, config Config, now time.Time) Row {
	// Row(results) = stat - spec
	// stat -> what a container has used over a period of time
	// spec -> what the container has requested and preserved in kubernetes
	// config -> ideal container needs?
	// Row -> a summary of a container and the ideal state it should have.

	resultRow := Row{
		Key:                 model.Key{Namespace: spec.Key.Namespace, Workload: spec.Key.Workload, Container: spec.Key.Container},
		Status:              StatusOK,
		HoursOfData:         stat.HoursOfData,
		MemRequestBytes:     spec.MemRequestBytes,
		MemMaxBytes:         max(stat.MemMax, spec.MemRequestBytes),
		MemWasteBytes:       max(0, spec.MemRequestBytes-stat.MemMax),
		MemWastePct:         max(0, float64(spec.MemRequestBytes)-float64(stat.MemMax)) / float64(spec.MemRequestBytes) * 100,
		MemRecommendedBytes: int64(stat.MemMax * (1.0 + int64(stat.MemAvg)) / 10),
		CPURequestMilli:     spec.CPURequestMilli,
		CPUMaxMilli:         stat.CPUMax,
		CPUWasteMilli:       max(0, spec.CPURequestMilli-stat.CPUMax),
		CPUWastePct:         max(0, float64(spec.CPURequestMilli)-float64(stat.CPUMax)) / float64(spec.CPURequestMilli) * 100,
		CPURecommendedMilli: stat.CPUMax * (1.0 + int64(stat.CPUAvg)) / 10,
	}

	if resultRow.CPUWastePct >= 20 || resultRow.MemWastePct >= 20 {
		// memory or CPU waste % >= warn     -> over-provisioned
		resultRow.Status = StatusOver
	} else if float64(stat.MemMax) > float64(spec.MemRequestBytes) {
		// memory max > memory request       -> under-provisioned  (memory only: CPU bursting is normal)
		resultRow.Status = StatusUnder
	}

	return resultRow
}
