package admin

import (
	"testing"

	"tspages/internal/analytics"
	"tspages/internal/storage"
)

func TestComputeFieldSummary_AutoDetect(t *testing.T) {
	tests := []struct {
		name      string
		values    []string
		want      string
		isNumeric bool
	}{
		{
			name:      "numeric values",
			values:    []string{"1", "5", "3"},
			want:      "1–5, Ø 3",
			isNumeric: true,
		},
		{
			name:      "numeric with decimals",
			values:    []string{"1.5", "2.5", "4"},
			want:      "1.5–4, Ø 2.67",
			isNumeric: true,
		},
		{
			name:   "all unique strings",
			values: []string{"alice", "bob", "charlie"},
			want:   "",
		},
		{
			name:   "recurring categories",
			values: []string{"yes", "no", "yes", "yes"},
			want:   "2 unique",
		},
		{
			name:   "all empty",
			values: []string{"", "", ""},
			want:   "",
		},
		{
			name:   "mixed empty and values",
			values: []string{"a", "", "a"},
			want:   "1 unique",
		},
		{
			name:      "single numeric value",
			values:    []string{"42"},
			want:      "42",
			isNumeric: true,
		},
		{
			name:      "all same numeric value",
			values:    []string{"5", "5", "5"},
			want:      "5",
			isNumeric: true,
		},
		{
			name:      "negative numbers",
			values:    []string{"-10", "20", "5"},
			want:      "-10–20, Ø 5",
			isNumeric: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, isNumeric := computeFieldSummary(tt.values, "")
			if got != tt.want {
				t.Errorf("summary = %q, want %q", got, tt.want)
			}
			if isNumeric != tt.isNumeric {
				t.Errorf("isNumeric = %v, want %v", isNumeric, tt.isNumeric)
			}
		})
	}
}

func TestComputeFieldSummary_ExplicitAggregate(t *testing.T) {
	tests := []struct {
		name      string
		values    []string
		aggregate string
		want      string
	}{
		{
			name:      "unique",
			values:    []string{"a", "b", "a"},
			aggregate: "unique",
			want:      "2 unique",
		},
		{
			name:      "total",
			values:    []string{"a", "", "b"},
			aggregate: "total",
			want:      "3",
		},
		{
			name:      "count_empty",
			values:    []string{"a", "", "b"},
			aggregate: "count_empty",
			want:      "1",
		},
		{
			name:      "count_not_empty",
			values:    []string{"a", "", "b"},
			aggregate: "count_not_empty",
			want:      "2",
		},
		{
			name:      "percent_empty",
			values:    []string{"a", "", ""},
			aggregate: "percent_empty",
			want:      "67%",
		},
		{
			name:      "percent_not_empty",
			values:    []string{"a", "", "b"},
			aggregate: "percent_not_empty",
			want:      "67%",
		},
		{
			name:      "sum",
			values:    []string{"10", "20", "30"},
			aggregate: "sum",
			want:      "60",
		},
		{
			name:      "avg",
			values:    []string{"10", "20", "30"},
			aggregate: "avg",
			want:      "Ø 20",
		},
		{
			name:      "mean alias",
			values:    []string{"10", "20", "30"},
			aggregate: "mean",
			want:      "Ø 20",
		},
		{
			name:      "median odd",
			values:    []string{"1", "3", "2"},
			aggregate: "median",
			want:      "2",
		},
		{
			name:      "median even",
			values:    []string{"1", "4", "2", "3"},
			aggregate: "median",
			want:      "2.5",
		},
		{
			name:      "min",
			values:    []string{"5", "1", "9"},
			aggregate: "min",
			want:      "1",
		},
		{
			name:      "max",
			values:    []string{"5", "1", "9"},
			aggregate: "max",
			want:      "9",
		},
		{
			name:      "range",
			values:    []string{"5", "1", "9"},
			aggregate: "range",
			want:      "1–9",
		},
		{
			name:      "numeric with non-numeric skipped",
			values:    []string{"10", "abc", "30"},
			aggregate: "sum",
			want:      "40",
		},
		{
			name:      "all non-numeric for sum",
			values:    []string{"abc", "def"},
			aggregate: "sum",
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _ := computeFieldSummary(tt.values, tt.aggregate)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestComputeDateSummary(t *testing.T) {
	tests := []struct {
		name  string
		views []submissionView
		want  string
	}{
		{
			name:  "empty",
			views: nil,
			want:  "",
		},
		{
			name: "single submission",
			views: []submissionView{
				{ParsedFields: map[string]string{}},
			},
			want: "", // invalid CreatedAt
		},
		{
			name: "same day",
			views: makeViews("2026-03-02T14:00:00Z", "2026-03-02T10:00:00Z"),
			want: "Mar 2, 2026",
		},
		{
			name: "same year different days",
			views: makeViews("2026-03-05T10:00:00Z", "2026-03-01T10:00:00Z"),
			want: "Mar 1 – Mar 5, 2026",
		},
		{
			name: "different years",
			views: makeViews("2026-01-15T10:00:00Z", "2025-12-20T10:00:00Z"),
			want: "Dec 20, 2025 – Jan 15, 2026",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeDateSummary(tt.views)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// makeViews creates submissionViews with CreatedAt timestamps (latest first).
func makeViews(timestamps ...string) []submissionView {
	views := make([]submissionView, len(timestamps))
	for i, ts := range timestamps {
		views[i] = submissionView{ParsedFields: map[string]string{}}
		views[i].CreatedAt = ts
	}
	return views
}

func TestFieldAggregateFromConfig(t *testing.T) {
	ep := &storage.FormEndpoint{
		Fields: []storage.FormField{
			{Key: "rating", Aggregate: "avg"},
			{Key: "name"},
		},
	}

	if got := fieldAggregateFromConfig(ep, "rating"); got != "avg" {
		t.Errorf("got %q, want %q", got, "avg")
	}
	if got := fieldAggregateFromConfig(ep, "name"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := fieldAggregateFromConfig(ep, "unknown"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := fieldAggregateFromConfig(nil, "rating"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestStableFieldOrder(t *testing.T) {
	tests := []struct {
		name     string
		endpoint *storage.FormEndpoint
		keys     []string
		want     []string
	}{
		{
			name:     "no config, sorts alphabetically",
			endpoint: nil,
			keys:     []string{"message", "email", "name"},
			want:     []string{"email", "message", "name"},
		},
		{
			name: "config order first, extras sorted",
			endpoint: &storage.FormEndpoint{
				Fields: []storage.FormField{
					{Key: "name"},
					{Key: "email"},
				},
			},
			keys: []string{"message", "email", "name", "age"},
			want: []string{"name", "email", "age", "message"},
		},
		{
			name: "config keys not in data are skipped",
			endpoint: &storage.FormEndpoint{
				Fields: []storage.FormField{
					{Key: "name"},
					{Key: "phone"},
				},
			},
			keys: []string{"name", "email"},
			want: []string{"name", "email"},
		},
		{
			name:     "empty keys",
			endpoint: nil,
			keys:     nil,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stableFieldOrder(tt.endpoint, tt.keys)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got %v, want %v", got, tt.want)
					break
				}
			}
		})
	}
}

func TestParseSortParam(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []analytics.SortField
	}{
		{"empty", "", nil},
		{"single asc", "email", []analytics.SortField{{Column: "email"}}},
		{"single desc", "-date", []analytics.SortField{{Column: "date", Desc: true}}},
		{"two fields", "-date,email", []analytics.SortField{{Column: "date", Desc: true}, {Column: "email"}}},
		{"two desc", "-submitter,-date", []analytics.SortField{{Column: "submitter", Desc: true}, {Column: "date", Desc: true}}},
		{"max two", "-a,b,c", []analytics.SortField{{Column: "a", Desc: true}, {Column: "b"}}},
		{"bare minus ignored", "-", nil},
		{"spaces trimmed", " -date , email ", []analytics.SortField{{Column: "date", Desc: true}, {Column: "email"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSortParam(tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("got %v, want %v", got, tt.want)
					break
				}
			}
		})
	}
}

func TestFormatSortParam(t *testing.T) {
	tests := []struct {
		name  string
		sorts []analytics.SortField
		want  string
	}{
		{"nil", nil, ""},
		{"single asc", []analytics.SortField{{Column: "email"}}, "email"},
		{"single desc", []analytics.SortField{{Column: "date", Desc: true}}, "-date"},
		{"two fields", []analytics.SortField{{Column: "date", Desc: true}, {Column: "email"}}, "-date,email"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSortParam(tt.sorts)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSortHref(t *testing.T) {
	tests := []struct {
		name string
		sort []analytics.SortField
		col  string
		want string
	}{
		{
			name: "click unsorted column with no active sort",
			sort: nil,
			col:  "date",
			want: "?path=%2Fcontact&sort=-date",
		},
		{
			name: "click unsorted column pushes to primary",
			sort: []analytics.SortField{{Column: "date", Desc: true}},
			col:  "email",
			want: "?path=%2Fcontact&sort=-email%2C-date",
		},
		{
			name: "click desc column flips to asc",
			sort: []analytics.SortField{{Column: "date", Desc: true}},
			col:  "date",
			want: "?path=%2Fcontact&sort=date",
		},
		{
			name: "click asc column removes it",
			sort: []analytics.SortField{{Column: "date"}},
			col:  "date",
			want: "?path=%2Fcontact",
		},
		{
			name: "click asc primary with secondary shifts secondary up",
			sort: []analytics.SortField{{Column: "date"}, {Column: "email"}},
			col:  "date",
			want: "?path=%2Fcontact&sort=email",
		},
		{
			name: "click desc secondary flips to asc",
			sort: []analytics.SortField{{Column: "date", Desc: true}, {Column: "email", Desc: true}},
			col:  "email",
			want: "?path=%2Fcontact&sort=-date%2Cemail",
		},
		{
			name: "click new column with two sorts drops old secondary",
			sort: []analytics.SortField{{Column: "date", Desc: true}, {Column: "email"}},
			col:  "submitter",
			want: "?path=%2Fcontact&sort=-submitter%2C-date",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := SubmissionsPageData{CurrentPath: "/contact", Sort: tt.sort}
			got := d.SortHref(tt.col)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSortInfo(t *testing.T) {
	tests := []struct {
		name string
		sort []analytics.SortField
		col  string
		want SortState
	}{
		{
			name: "unsorted column",
			sort: []analytics.SortField{{Column: "date", Desc: true}},
			col:  "email",
			want: SortState{},
		},
		{
			name: "single sort desc",
			sort: []analytics.SortField{{Column: "date", Desc: true}},
			col:  "date",
			want: SortState{Desc: true, Position: ""},
		},
		{
			name: "single sort asc",
			sort: []analytics.SortField{{Column: "date"}},
			col:  "date",
			want: SortState{Asc: true, Position: ""},
		},
		{
			name: "multi-sort primary",
			sort: []analytics.SortField{{Column: "date", Desc: true}, {Column: "email"}},
			col:  "date",
			want: SortState{Desc: true, Position: "1"},
		},
		{
			name: "multi-sort secondary",
			sort: []analytics.SortField{{Column: "date", Desc: true}, {Column: "email"}},
			col:  "email",
			want: SortState{Asc: true, Position: "2"},
		},
		{
			name: "empty sort list",
			sort: nil,
			col:  "date",
			want: SortState{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := SubmissionsPageData{Sort: tt.sort}
			got := d.SortInfo(tt.col)
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseSortParam_RoundTrip(t *testing.T) {
	inputs := [][]analytics.SortField{
		{{Column: "date", Desc: true}},
		{{Column: "date", Desc: true}, {Column: "email"}},
		{{Column: "submitter"}, {Column: "date", Desc: true}},
	}
	for _, in := range inputs {
		s := formatSortParam(in)
		got := parseSortParam(s)
		if len(got) != len(in) {
			t.Fatalf("round-trip %q: got %v, want %v", s, got, in)
		}
		for i := range got {
			if got[i] != in[i] {
				t.Errorf("round-trip %q: got %v, want %v", s, got, in)
				break
			}
		}
	}
}

func TestFormatNum(t *testing.T) {
	tests := []struct {
		in   float64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{-5, "-5"},
		{3.14, "3.14"},
		{2.5, "2.5"},
		{2.10, "2.1"},
		{1.0 / 3.0, "0.33"},
	}
	for _, tt := range tests {
		got := formatNum(tt.in)
		if got != tt.want {
			t.Errorf("formatNum(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
