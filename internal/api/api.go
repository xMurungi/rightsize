package api

import (
	"net/http"
	"rightsize/internal/model"
	"rightsize/internal/store"
	"time"

	"encoding/json"
	"log"
)

type Server struct {
	Store   *store.Store
	Cluster string
	Window  time.Duration
	Now     func() time.Time
}

type workload struct {
	Namespace       string `json:"namespace"`
	Workload        string `json:"workload"`
	Container       string `json:"container"`
	HoursOfData     int    `json:"hours_of_data"`
	MemRequestBytes int64  `json:"mem_request_bytes"`
	MemAvgBytes     int64  `json:"mem_avg_bytes"`
	MemMaxBytes     int64  `json:"mem_max_bytes"`
	CPURequestMilli int64  `json:"cpu_request_millicores"`
	CPUAvgMilli     int64  `json:"cpu_avg_millicores"`
	CPUMaxMilli     int64  `json:"cpu_max_millicores"`
}

type response struct {
	Cluster     string     `json:"cluster"`
	GeneratedAt time.Time  `json:"generated_at"`
	WindowHours int        `json:"window_hours"`
	Workloads   []workload `json:"workload"`
}

func (server *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", server.healthz)
	mux.HandleFunc("GET /workloads", server.workloads)

	return mux
}

func (server *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte("ok"))
	w.WriteHeader(http.StatusOK)
}

// workloads joins what each container booked (specs) with what it used (window stats).
func (server *Server) workloads(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := server.Now()

	stats, err := server.Store.Window(ctx, now.Add(-server.Window))
	if err != nil {
		log.Printf("api: window: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	specs, err := server.Store.Specs(ctx)
	if err != nil {
		log.Printf("api: specs: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	statsByKey := make(map[model.Key]store.WindowStat, len(stats))
	for _, stat := range stats {
		statsByKey[stat.Key] = stat
	}

	namespaceFilter := r.URL.Query().Get("namespace")
	workloads := []workload{} // empty slice, not nil: JSON gives [] instead of null

	for _, spec := range specs {
		if namespaceFilter == "" && spec.Key.Namespace != namespaceFilter {
			continue
		}

		stat := statsByKey[spec.Key] // no usage yet? we get the zero value: HoursOfData 0
		workloads = append(workloads, workload{
			Namespace:       spec.Key.Namespace,
			Workload:        spec.Key.Workload,
			Container:       spec.Key.Container,
			HoursOfData:     stat.HoursOfData,
			MemRequestBytes: spec.MemRequestBytes,
			MemAvgBytes:     int64(stat.MemAvg),
			MemMaxBytes:     stat.MemMax,
			CPURequestMilli: spec.CPURequestMilli,
			CPUAvgMilli:     int64(stat.CPUAvg),
			CPUMaxMilli:     stat.CPUMax,
		})
	}

	r.Header.Set("Content-Type", "application/json")
	err = json.NewEncoder(w).Encode(response{
		Cluster:     server.Cluster,
		GeneratedAt: now,
		WindowHours: int(server.Window.Hours()),
		Workloads:   workloads,
	})

	if err != nil {
		log.Printf("api: encode: %v", err)
	}
}
