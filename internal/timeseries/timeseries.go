// Package timeseries provides shared time-bucketing utilities for
// SQLite-based analytics queries.
package timeseries

import (
	"fmt"
	"time"
)

// BucketSQL returns an SQL expression that truncates the given column to a
// bucket boundary using epoch-based rounding. The caller must supply the step
// size in seconds as two consecutive query parameters (the value is used
// twice in the expression).
func BucketSQL(column string) string {
	return fmt.Sprintf(
		`strftime('%%Y-%%m-%%dT%%H:%%M:%%SZ', (CAST(strftime('%%s', %s) AS INTEGER) / ?) * ?, 'unixepoch')`,
		column,
	)
}

// BucketStep returns the largest "nice" step that produces at least 64
// buckets over the given time range.
func BucketStep(from, to time.Time) time.Duration {
	d := to.Sub(from)
	steps := []time.Duration{
		24 * time.Hour,
		12 * time.Hour,
		8 * time.Hour,
		6 * time.Hour,
		4 * time.Hour,
		2 * time.Hour,
		time.Hour,
		30 * time.Minute,
		15 * time.Minute,
	}
	for _, s := range steps {
		if d/s >= 64 {
			return s
		}
	}
	return 15 * time.Minute
}

// FillBuckets takes sparse query results and returns a complete series with
// zero-valued entries for gaps between from and to.
//
// getTime extracts the time key from a bucket; zero returns an empty bucket
// for the given time key.
func FillBuckets[T any](sparse []T, from, to time.Time, getTime func(T) string, zero func(string) T) []T {
	if from.IsZero() && len(sparse) > 0 {
		t, err := time.Parse(time.RFC3339, getTime(sparse[0]))
		if err == nil {
			from = t
		}
	}
	if from.IsZero() {
		return sparse
	}

	step := BucketStep(from, to)
	from = from.UTC().Truncate(step)

	lookup := make(map[string]T, len(sparse))
	for _, b := range sparse {
		lookup[getTime(b)] = b
	}

	var out []T
	for t := from; !t.After(to.UTC()); t = t.Add(step) {
		key := t.Format(time.RFC3339)
		if b, ok := lookup[key]; ok {
			out = append(out, b)
		} else {
			out = append(out, zero(key))
		}
	}
	return out
}
