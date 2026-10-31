package model

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
