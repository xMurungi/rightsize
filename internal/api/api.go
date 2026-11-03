package api

import (
	"net/http"
	"rightsize/internal/analyzer"
	"rightsize/internal/model"
	"rightsize/internal/store"
	"sort"
	"time"

	"encoding/json"
	"log"
)

type Server struct {
	Store   *store.Store
	Cluster string
	Window  time.Duration
	Config  analyzer.Config
	Now     func() time.Time
}

type workload struct {
	Namespace   string `json:"namespace"`
	Workload    string `json:"workload"`
	Container   string `json:"container"`
	Status      string `json:"status"`
	HoursOfData int    `json:"hours_of_data"`

	MemRequestBytes     int64   `json:"mem_request_bytes"`
	MemAvgBytes         int64   `json:"mem_avg_bytes"`
	MemMaxBytes         int64   `json:"mem_max_bytes"`
	MemWasteBytes       int64   `json:"mem_waste_bytes"`
	MemWastePct         float64 `json:"mem_waste_pct"`
	MemRecommendedBytes int64   `json:"mem_recommended_bytes"`

	CPURequestMilli     int64   `json:"cpu_request_millicores"`
	CPUAvgMilli         int64   `json:"cpu_avg_millicores"`
	CPUMaxMilli         int64   `json:"cpu_max_millicores"`
	CPUWasteMilli       int64   `json:"cpu_waste_millicores"`
	CPUWastePct         float64 `json:"cpu_waste_pct"`
	CPURecommendedMilli int64   `json:"cpu_recommended_millicores"`
}

type response struct {
	Cluster     string     `json:"cluster"`
	GeneratedAt time.Time  `json:"generated_at"`
	WindowHours int        `json:"window_hours"`
	Workloads   []workload `json:"workloads"`
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
	statusFilter := r.URL.Query().Get("status")

	workloads := []workload{}
	for _, spec := range specs {
		if namespaceFilter != "" && spec.Key.Namespace != namespaceFilter {
			continue
		}

		stat := statsByKey[spec.Key]
		row := analyzer.Classify(spec, stat, server.Config, now)

		if statusFilter != "" && row.Status != statusFilter {
			continue
		}

		workloads = append(workloads, workload{
			Namespace:           row.Key.Namespace,
			Workload:            row.Key.Workload,
			Container:           row.Key.Container,
			Status:              row.Status,
			HoursOfData:         row.HoursOfData,
			MemRequestBytes:     row.MemRequestBytes,
			MemAvgBytes:         int64(stat.MemAvg),
			MemMaxBytes:         row.MemMaxBytes,
			MemWasteBytes:       row.MemWasteBytes,
			MemWastePct:         row.MemWastePct,
			MemRecommendedBytes: row.MemRecommendedBytes,
			CPURequestMilli:     row.CPURequestMilli,
			CPUAvgMilli:         int64(stat.CPUAvg),
			CPUMaxMilli:         row.CPUMaxMilli,
			CPUWasteMilli:       row.CPUWasteMilli,
			CPUWastePct:         row.CPUWastePct,
			CPURecommendedMilli: row.CPURecommendedMilli,
		})
	}

	// Biggest memory waste first: the list people read from the top.
	sort.SliceStable(workloads, func(i, j int) bool {
		return workloads[i].MemWasteBytes > workloads[j].MemWasteBytes
	})

	w.Header().Set("Content-Type", "application/json")
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
