package web

import (
	"testing"
	"time"
)

func TestSubtractISO8601(t *testing.T) {
	now := time.Date(2025, 3, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		input string
		want  time.Time
		ok    bool
	}{
		{"PT24H", now.Add(-24 * time.Hour), true},
		{"PT1H", now.Add(-time.Hour), true},
		{"P7D", now.AddDate(0, 0, -7), true},
		{"P30D", now.AddDate(0, 0, -30), true},
		{"P1M", now.AddDate(0, -1, 0), true},
		{"P1Y", now.AddDate(-1, 0, 0), true},
		{"P1Y2M3DT4H5M6S", now.AddDate(-1, -2, -3).Add(-4*time.Hour - 5*time.Minute - 6*time.Second), true},
		{"P2W", now.AddDate(0, 0, -14), true},
		{"", time.Time{}, false},
		{"24h", time.Time{}, false},
		{"P", time.Time{}, false},
		{"PT", time.Time{}, false},
		{"P0D", time.Time{}, false},
		{"PXD", time.Time{}, false},
	}
	for _, tt := range tests {
		got, ok := subtractISO8601(now, tt.input)
		if ok != tt.ok {
			t.Errorf("subtractISO8601(%q): ok = %v, want %v", tt.input, ok, tt.ok)
			continue
		}
		if ok && !got.Equal(tt.want) {
			t.Errorf("subtractISO8601(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
