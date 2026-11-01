package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"rightsize/internal/aggregator"
	"rightsize/internal/model"
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS usage_hourly (
	hour      INTEGER NOT NULL,  -- unix seconds, start of the hour (UTC)
	namespace TEXT    NOT NULL,
	workload  TEXT    NOT NULL,
	container TEXT    NOT NULL,
	samples   INTEGER NOT NULL,
	cpu_avg   REAL    NOT NULL,  -- millicores
	cpu_max   INTEGER NOT NULL,
	mem_avg   REAL    NOT NULL,  -- bytes
	mem_max   INTEGER NOT NULL,
	PRIMARY KEY (hour, namespace, workload, container)
);
CREATE TABLE IF NOT EXISTS requests (
	namespace     TEXT    NOT NULL,
	workload      TEXT    NOT NULL,
	container     TEXT    NOT NULL,
	cpu_request   INTEGER NOT NULL,
	mem_request   INTEGER NOT NULL,
	cpu_limit     INTEGER NOT NULL,
	mem_limit     INTEGER NOT NULL,
	restarts      INTEGER NOT NULL,
	oom_last_seen INTEGER NOT NULL DEFAULT 0,  -- unix seconds, 0 = never
	updated_at    INTEGER NOT NULL,
	PRIMARY KEY (namespace, workload, container)
);`

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}

	return &Store{db: db}, nil
}

func (store *Store) Close() error {
	return store.db.Close()
}

// SaveHourly writes hourly rows. If a row for the same hour and container
// already exists (for example after a restart mid-hour), the two are merged.
func (store *Store) SaveHourly(ctx context.Context, rows []aggregator.Hourly) error {

	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer transaction.Rollback() // harmless after a successful Commit

	for _, row := range rows {
		_, err := transaction.ExecContext(ctx, `
			INSERT INTO usage_hourly (hour, namespace, workload, container, samples, cpu_avg, cpu_max, mem_avg, mem_max)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (hour, namespace, workload, container) DO UPDATE SET
				cpu_avg = (cpu_avg * samples + excluded.cpu_avg * excluded.samples) / (samples + excluded.samples),
				mem_avg = (mem_avg * samples + excluded.mem_avg * excluded.samples) / (samples + excluded.samples),
				cpu_max = MAX(cpu_max, excluded.cpu_max),
				mem_max = MAX(mem_max, excluded.mem_max),
				samples = samples + excluded.samples`,
			row.Hour.Unix(), row.Key.Namespace, row.Key.Workload, row.Key.Container,
			row.Samples, row.CPUAvg, row.CPUMax, row.MemAvg, row.MemMax)
		if err != nil {
			return fmt.Errorf("save %v: %w", row.Key, err)
		}
	}
	return transaction.Commit()
}

// WindowStat is the level-3 result: one container summarised over many hours.
type WindowStat struct {
	Key         model.Key
	HoursOfData int
	CPUAvg      float64
	CPUMax      int64
	MemAvg      float64
	MemMax      int64
}

func (store *Store) Window(ctx context.Context, since time.Time) ([]WindowStat, error) {
	rows, err := store.db.QueryContext(ctx, `
		SELECT namespace, workload, container,
		       COUNT(*),
		       SUM(cpu_avg * samples) / SUM(samples),
		       MAX(cpu_max),
		       SUM(mem_avg * samples) / SUM(samples),
		       MAX(mem_max)
		FROM usage_hourly
		WHERE hour >= ?
		GROUP BY namespace, workload, container
		ORDER BY namespace, workload, container`, since.Unix())
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var stats []WindowStat

	for rows.Next() {
		var stat WindowStat
		err := rows.Scan(&stat.Key.Namespace, &stat.Key.Workload, &stat.Key.Container,
			&stat.HoursOfData, &stat.CPUAvg, &stat.CPUMax, &stat.MemAvg, &stat.MemMax)
		if err != nil {
			return nil, err
		}
		stats = append(stats, stat)
	}

	return stats, rows.Err()
}

func (store *Store) Prune(ctx context.Context, before time.Time) error {

	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	
	defer transaction.Rollback()

	if _, err := transaction.ExecContext(ctx,
		`DELETE FROM usage_hourly WHERE hour < ?`, before.Unix()); err != nil {
		return fmt.Errorf("prune usage_hourly: %w", err)
	}

	if _, err := transaction.ExecContext(ctx,
		`DELETE FROM requests WHERE updated_at < ?`, before.Unix()); err != nil {
		return fmt.Errorf("prune requests: %w", err)
	}

	return transaction.Commit()
}

// SaveSpecs stores the latest requests. Unlike usage, requests are overwritten:
// the newest value wins. The one exception is the last-OOM time, which only
// moves forward, so a redeploy (a fresh pod with a clean status) can't make us forget.
func (store *Store) SaveSpecs(ctx context.Context, specs []model.Spec, now time.Time) error {

	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer transaction.Rollback()

	for _, spec := range specs {
		var lastOOM int64

		if !spec.LastOOM.IsZero() {
			lastOOM = spec.LastOOM.Unix()
		}

		_, err := transaction.ExecContext(ctx, `
			INSERT INTO requests (namespace, workload, container, cpu_request, mem_request,cpu_limit, mem_limit, restarts, oom_last_seen, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (namespace, workload, container) DO UPDATE SET
				cpu_request   = excluded.cpu_request,
				mem_request   = excluded.mem_request,
				cpu_limit     = excluded.cpu_limit,
				mem_limit     = excluded.mem_limit,
				restarts      = excluded.restarts,
				oom_last_seen = MAX(oom_last_seen, excluded.oom_last_seen),
				updated_at    = excluded.updated_at`,
			spec.Key.Namespace, spec.Key.Namespace, spec.Key.Container, spec.CPURequestMilli, spec.MemRequestBytes, spec.CPULimitMilli, spec.MemLimitBytes, spec.Restarts, lastOOM, now.Unix())

		if err != nil {
			return fmt.Errorf("save spec %v: %w", spec.Key, err)
		}
	}
	return transaction.Commit()
}

func (store *Store) Specs(ctx context.Context) ([]model.Spec, error) {

	rows, err := store.db.QueryContext(ctx, `
		SELECT namespace, workload, container, cpu_request, mem_request, cpu_limit, mem_limit, restarts, oom_last_seen
		FROM requests
		ORDER BY namespace, workload, container`)
	if err != nil {
		return nil, err
	}

	defer rows.Close()

	var specs []model.Spec
	for rows.Next() {
		var spec model.Spec
		var lastOOM int64

		err := rows.Scan(&spec.Key.Namespace, &spec.Key.Workload, &spec.Key.Container, &spec.CPURequestMilli,
			&spec.MemRequestBytes, &spec.CPULimitMilli, &spec.MemLimitBytes, &spec.Restarts, &lastOOM)

		if err != nil {
			return nil, err
		}

		if lastOOM > 0 {
			spec.LastOOM = time.Unix(lastOOM, 0).UTC()
		}

		specs = append(specs, spec)
	}
	return specs, rows.Err()
}
