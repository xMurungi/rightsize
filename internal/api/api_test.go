package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"rightsize/internal/aggregator"
	"rightsize/internal/model"
	"rightsize/internal/store"
)

func TestWorkloadsJoinsBookedWithUsed(t *testing.T) {
	ctx := context.Background()
	hourlyStore, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer hourlyStore.Close()

	oldKey := model.Key{Namespace: "payments", Workload: "api", Container: "app"}
	newKey := model.Key{Namespace: "payments", Workload: "brand-new", Container: "app"}
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	specs := []model.Spec{
		{Key: oldKey, MemRequestBytes: 1000},
		{Key: newKey, MemRequestBytes: 500},
	}
	if err := hourlyStore.SaveSpecs(ctx, specs, now); err != nil {
		t.Fatal(err)
	}
	usage := []aggregator.Hourly{{Hour: now.Add(-time.Hour), Key: oldKey, Samples: 60, MemAvg: 200, MemMax: 300}}
	if err := hourlyStore.SaveHourly(ctx, usage); err != nil {
		t.Fatal(err)
	}

	server := &Server{Store: hourlyStore, Cluster: "test", Window: 7 * 24 * time.Hour, Now: func() time.Time { return now }}
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest("GET", "/workloads", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, want 200", recorder.Code)
	}
	var got response
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Workloads) != 2 {
		t.Fatalf("got %d workloads, want 2", len(got.Workloads))
	}
	// Ordered by namespace, workload, container: "api" comes before "brand-new".
	if got.Workloads[0].HoursOfData != 1 || got.Workloads[0].MemMaxBytes != 300 || got.Workloads[0].MemRequestBytes != 1000 {
		t.Errorf("first workload = %+v, want 1 hour, max 300, request 1000", got.Workloads[0])
	}
	if got.Workloads[1].HoursOfData != 0 || got.Workloads[1].MemMaxBytes != 0 {
		t.Errorf("brand-new workload = %+v, want zero hours of data and zero max", got.Workloads[1])
	}
}
