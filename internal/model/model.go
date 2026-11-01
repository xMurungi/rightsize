package model

import "time"

// Key identifies one container of one workload. We use the workload name
// (Deployment, StatefulSet...) not the pod name, because pod names change
// on every restart and we want one continuous history.
type Key struct {
	Namespace string
	Workload  string
	Container string
}

// Usage is one reading. Integers in the smallest unit (millicores, bytes):
// floats accumulate rounding error, and Kubernetes itself speaks integers.
type Usage struct {
	Key      Key
	CPUMilli int64
	MemBytes int64
}

// Spec is what a container booked (requests and limits), plus warning signs.
type Spec struct {
	Key             Key
	CPURequestMilli int64
	MemRequestBytes int64
	CPULimitMilli   int64
	MemLimitBytes   int64
	Restarts        int64
	LastOOM         time.Time // zero value = never OOMKilled
}

// Snapshot is everything the source sees at one moment.
type Snapshot struct {
	Usages []Usage
	Specs  []Spec
}
