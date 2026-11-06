package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"rightsize/internal/analyzer"
	"rightsize/internal/api"
	"rightsize/internal/collector"
	"rightsize/internal/source"
	"rightsize/internal/store"
	"syscall"
	"time"
)

func main() {
	dbPath := flag.String("db", "rightsize.db", "path to the SQLite file")
	addr := flag.String("addr", ":8080", "HTTP listen address")
	cluster := flag.String("cluster", "local", "cluster name shown in responses")
	interval := flag.Duration("interval", time.Minute, "how often to read metrics")
	windowDays := flag.Int("window-days", 7, "days of history to compare")

	margin := flag.Float64("margin", 0.2, "headroom added to max usage in recommendations (0.2 = +20%)")
	minHours := flag.Int("min-hours", 24, "hours of data needed before results are trusted")

	retentionDays := flag.Int("retention-days", 14, "days of hourly rows to keep")
	sourceName := flag.String("source", "fake", "where readings come from: fake | kube")

	flag.Parse()

	// ctx is cancelled on Ctrl-C or SIGTERM (what Kubernetes sends on shutdown).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hourlyStore, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer hourlyStore.Close()

	var readings source.Source
	switch *sourceName {
	case "fake":
		readings = source.NewFake(time.Now().UnixNano())
	case "kube":
		kubeConfig, err := source.LoadKubeConfig()
		if err != nil {
			log.Fatalf("kubernetes config: %v", err)
		}
		log.Printf("reading from Kubernetes API server %s", kubeConfig.Host) // so you always see WHICH cluster
		readings, err = source.NewKube(kubeConfig)

		if err != nil {
			log.Fatalf("kubernetes client: %v", err)
		}
	default:
		log.Fatalf("unknown -source %q (want fake or kube)", *sourceName)
	}

	collectorLoop := collector.New(readings, hourlyStore, *interval)
	collectorLoop.Retention = time.Duration(*retentionDays) * 24 * time.Hour

	collectorDone := make(chan struct{})

	go func() {
		defer close(collectorDone)
		collectorLoop.Run(ctx)
	}()

	apiServer := &api.Server{
		Store:   hourlyStore,
		Cluster: *cluster,
		Window:  time.Duration(*windowDays) * 24 * time.Hour,
		Now:     func() time.Time { return time.Now().UTC() },
		Config: analyzer.Config{
			Margin:       *margin,
			MinHours:     *minHours,
			OOMWindow:    7 * 24 * time.Hour,
			WasteWarnPct: 30,
		},
	}

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdownCtx)
	}()

	log.Printf("listening on %s", *addr)

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("http server: %v", err)
	}

	<-collectorDone // wait for the collector's final save before exiting
	log.Println("stopped cleanly")
}
