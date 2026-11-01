package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
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
	flag.Parse()

	// ctx is cancelled on Ctrl-C or SIGTERM (what Kubernetes sends on shutdown).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hourlyStore, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer hourlyStore.Close()

	collectorLoop := collector.New(source.NewFake(time.Now().UnixNano()), hourlyStore, *interval)
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
