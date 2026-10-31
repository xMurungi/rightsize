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

type Store struct{
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
