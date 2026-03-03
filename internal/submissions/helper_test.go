package submissions

import (
	"testing"

	"tspages/internal/storage"
)

// region HumanizePath

func TestHumanizePath(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"", "Home"},
		{"/", "Home"},
		{"//", "Home"},
		{"/contact", "Contact"},
		{"/contact/", "Contact"},
		{"/api/contact-us", "Contact Us"},
		{"/feedback_form", "Feedback Form"},
		{"/forms/sign-up", "Sign Up"},
		{"/deep/nested/path", "Path"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := HumanizePath(tt.path)
			if got != tt.want {
				t.Errorf("HumanizePath(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}

// region HumanizeKey

func TestHumanizeKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"", ""},
		{"name", "Name"},
		{"first_name", "First Name"},
		{"first-name", "First Name"},
		{"firstName", "First Name"},
		{"userID", "User ID"},
		{"HTMLContent", "HTML Content"},
		{"emailAddress", "Email Address"},
		{"created_at", "Created At"},
		{"a", "A"},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got := HumanizeKey(tt.key)
			if got != tt.want {
				t.Errorf("HumanizeKey(%q) = %q, want %q", tt.key, got, tt.want)
			}
		})
	}
}

// region parseSortParam

func TestParseSortParam(t *testing.T) {
	tests := []struct {
		raw  string
		want []SortField
	}{
		{"", nil},
		{"-date", []SortField{{Column: "date", Desc: true}}},
		{"email", []SortField{{Column: "email", Desc: false}}},
		{"-date,email", []SortField{{Column: "date", Desc: true}, {Column: "email", Desc: false}}},
		{"-date,-name", []SortField{{Column: "date", Desc: true}, {Column: "name", Desc: true}}},
		// max 2 fields
		{"-date,email,extra", []SortField{{Column: "date", Desc: true}, {Column: "email", Desc: false}}},
		// whitespace trimming
		{" -date , email ", []SortField{{Column: "date", Desc: true}, {Column: "email", Desc: false}}},
		// bare minus is skipped
		{"-", nil},
		// empty parts skipped
		{",email", []SortField{{Column: "email", Desc: false}}},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got := parseSortParam(tt.raw)
			if len(got) != len(tt.want) {
				t.Fatalf("parseSortParam(%q) returned %d fields, want %d", tt.raw, len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("field[%d] = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// region formatSortParam

func TestFormatSortParam(t *testing.T) {
	tests := []struct {
		sorts []SortField
		want  string
	}{
		{nil, ""},
		{[]SortField{{Column: "date", Desc: true}}, "-date"},
		{[]SortField{{Column: "email", Desc: false}}, "email"},
		{[]SortField{{Column: "date", Desc: true}, {Column: "email", Desc: false}}, "-date,email"},
	}
	for _, tt := range tests {
		got := formatSortParam(tt.sorts)
		if got != tt.want {
			t.Errorf("formatSortParam(%+v) = %q, want %q", tt.sorts, got, tt.want)
		}
	}
}

func TestParseSortRoundTrip(t *testing.T) {
	tests := []string{"-date", "email", "-date,email", "-name,-score"}
	for _, raw := range tests {
		parsed := parseSortParam(raw)
		got := formatSortParam(parsed)
		if got != raw {
			t.Errorf("roundtrip(%q) = %q", raw, got)
		}
	}
}

// region computeFieldSummary

func TestComputeFieldSummary_NumericRange(t *testing.T) {
	values := []string{"10", "20", "30"}
	summary, isNumeric := computeFieldSummary(values, "")
	if !isNumeric {
		t.Error("expected numeric")
	}
	// min=10, max=30, avg=20 → "10–30, Ø 20"
	want := "10\u201330, \u00d8 20"
	if summary != want {
		t.Errorf("got %q, want %q", summary, want)
	}
}

func TestComputeFieldSummary_NumericAllSame(t *testing.T) {
	values := []string{"42", "42", "42"}
	summary, isNumeric := computeFieldSummary(values, "")
	if !isNumeric {
		t.Error("expected numeric")
	}
	if summary != "42" {
		t.Errorf("got %q, want %q", summary, "42")
	}
}

func TestComputeFieldSummary_StringAllUnique(t *testing.T) {
	values := []string{"alice", "bob", "charlie"}
	summary, isNumeric := computeFieldSummary(values, "")
	if isNumeric {
		t.Error("expected non-numeric")
	}
	// All unique → empty summary.
	if summary != "" {
		t.Errorf("got %q, want empty", summary)
	}
}

func TestComputeFieldSummary_StringSomeDuplicates(t *testing.T) {
	values := []string{"red", "blue", "red", "green"}
	summary, isNumeric := computeFieldSummary(values, "")
	if isNumeric {
		t.Error("expected non-numeric")
	}
	if summary != "3 unique" {
		t.Errorf("got %q, want %q", summary, "3 unique")
	}
}

func TestComputeFieldSummary_EmptyValues(t *testing.T) {
	values := []string{"", "", ""}
	summary, isNumeric := computeFieldSummary(values, "")
	if isNumeric {
		t.Error("expected non-numeric")
	}
	if summary != "" {
		t.Errorf("got %q, want empty", summary)
	}
}

func TestComputeFieldSummary_ExplicitAggregate_Sum(t *testing.T) {
	values := []string{"10", "20", "30"}
	summary, isNumeric := computeFieldSummary(values, "sum")
	if !isNumeric {
		t.Error("expected numeric")
	}
	if summary != "60" {
		t.Errorf("got %q, want %q", summary, "60")
	}
}

func TestComputeFieldSummary_ExplicitAggregate_Avg(t *testing.T) {
	values := []string{"10", "20", "30"}
	summary, _ := computeFieldSummary(values, "avg")
	want := "\u00d8 20"
	if summary != want {
		t.Errorf("got %q, want %q", summary, want)
	}
}

func TestComputeFieldSummary_ExplicitAggregate_Median_Odd(t *testing.T) {
	values := []string{"1", "3", "2"}
	summary, _ := computeFieldSummary(values, "median")
	if summary != "2" {
		t.Errorf("got %q, want %q", summary, "2")
	}
}

func TestComputeFieldSummary_ExplicitAggregate_Median_Even(t *testing.T) {
	values := []string{"1", "2", "3", "4"}
	summary, _ := computeFieldSummary(values, "median")
	if summary != "2.5" {
		t.Errorf("got %q, want %q", summary, "2.5")
	}
}

func TestComputeFieldSummary_ExplicitAggregate_Min(t *testing.T) {
	values := []string{"5", "1", "10"}
	summary, _ := computeFieldSummary(values, "min")
	if summary != "1" {
		t.Errorf("got %q, want %q", summary, "1")
	}
}

func TestComputeFieldSummary_ExplicitAggregate_Max(t *testing.T) {
	values := []string{"5", "1", "10"}
	summary, _ := computeFieldSummary(values, "max")
	if summary != "10" {
		t.Errorf("got %q, want %q", summary, "10")
	}
}

func TestComputeFieldSummary_ExplicitAggregate_Range(t *testing.T) {
	values := []string{"5", "1", "10"}
	summary, _ := computeFieldSummary(values, "range")
	want := "1\u201310"
	if summary != want {
		t.Errorf("got %q, want %q", summary, want)
	}
}

func TestComputeFieldSummary_ExplicitAggregate_Unique(t *testing.T) {
	values := []string{"red", "blue", "red"}
	summary, _ := computeFieldSummary(values, "unique")
	if summary != "2 unique" {
		t.Errorf("got %q, want %q", summary, "2 unique")
	}
}

func TestComputeFieldSummary_ExplicitAggregate_Total(t *testing.T) {
	values := []string{"a", "", "b"}
	summary, _ := computeFieldSummary(values, "total")
	if summary != "3" {
		t.Errorf("got %q, want %q", summary, "3")
	}
}

func TestComputeFieldSummary_ExplicitAggregate_CountEmpty(t *testing.T) {
	values := []string{"a", "", "b"}
	summary, _ := computeFieldSummary(values, "count_empty")
	if summary != "1" {
		t.Errorf("got %q, want %q", summary, "1")
	}
}

func TestComputeFieldSummary_ExplicitAggregate_CountNotEmpty(t *testing.T) {
	values := []string{"a", "", "b"}
	summary, _ := computeFieldSummary(values, "count_not_empty")
	if summary != "2" {
		t.Errorf("got %q, want %q", summary, "2")
	}
}

func TestComputeFieldSummary_ExplicitAggregate_PercentEmpty(t *testing.T) {
	values := []string{"a", "", "b", ""}
	summary, _ := computeFieldSummary(values, "percent_empty")
	if summary != "50%" {
		t.Errorf("got %q, want %q", summary, "50%")
	}
}

func TestComputeFieldSummary_ExplicitAggregate_PercentNotEmpty(t *testing.T) {
	values := []string{"a", "", "b", ""}
	summary, _ := computeFieldSummary(values, "percent_not_empty")
	if summary != "50%" {
		t.Errorf("got %q, want %q", summary, "50%")
	}
}

func TestComputeFieldSummary_ExplicitAggregate_NonNumericWithSumFallback(t *testing.T) {
	values := []string{"abc", "def"}
	summary, isNumeric := computeFieldSummary(values, "sum")
	if isNumeric {
		t.Error("expected non-numeric")
	}
	// Non-parseable values: computeNumericAggregate returns "".
	if summary != "" {
		t.Errorf("got %q, want empty", summary)
	}
}

// region computeDateSummary

func TestComputeDateSummary_Empty(t *testing.T) {
	got := computeDateSummary(nil)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestComputeDateSummary_SingleDate(t *testing.T) {
	views := []submissionView{
		{Submission: Submission{CreatedAt: "2025-06-15T10:00:00Z"}},
		{Submission: Submission{CreatedAt: "2025-06-15T14:00:00Z"}},
	}
	got := computeDateSummary(views)
	if got != "Jun 15, 2025" {
		t.Errorf("got %q, want %q", got, "Jun 15, 2025")
	}
}

func TestComputeDateSummary_SameYear(t *testing.T) {
	views := []submissionView{
		{Submission: Submission{CreatedAt: "2025-03-01T10:00:00Z"}},
		{Submission: Submission{CreatedAt: "2025-06-15T14:00:00Z"}},
	}
	got := computeDateSummary(views)
	want := "Mar 1 \u2013 Jun 15, 2025"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestComputeDateSummary_DifferentYears(t *testing.T) {
	views := []submissionView{
		{Submission: Submission{CreatedAt: "2024-12-01T10:00:00Z"}},
		{Submission: Submission{CreatedAt: "2025-03-15T14:00:00Z"}},
	}
	got := computeDateSummary(views)
	want := "Dec 1, 2024 \u2013 Mar 15, 2025"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// region stableFieldOrder

func TestStableFieldOrder_NoEndpoint(t *testing.T) {
	keys := []string{"zebra", "apple", "mango"}
	got := stableFieldOrder(nil, keys)
	want := []string{"apple", "mango", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStableFieldOrder_WithEndpoint(t *testing.T) {
	ep := &storage.FormEndpoint{
		Fields: []storage.FormField{
			{Key: "name"},
			{Key: "email"},
			{Key: "phone"},
		},
	}
	keys := []string{"zebra", "email", "name", "apple"}
	got := stableFieldOrder(ep, keys)
	// Config-declared first (in order): name, email. Then remaining sorted: apple, zebra.
	want := []string{"name", "email", "apple", "zebra"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestStableFieldOrder_Empty(t *testing.T) {
	got := stableFieldOrder(nil, nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

// region formatNum

func TestFormatNum(t *testing.T) {
	tests := []struct {
		v    float64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{-7, "-7"},
		{3.14, "3.14"},
		{3.10, "3.1"},
		{1.00, "1"},
		{100.50, "100.5"},
	}
	for _, tt := range tests {
		got := formatNum(tt.v)
		if got != tt.want {
			t.Errorf("formatNum(%v) = %q, want %q", tt.v, got, tt.want)
		}
	}
}

// region csvSanitize

func TestCsvSanitize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"", ""},
		{"=cmd", "'=cmd"},
		{"+cmd", "'+cmd"},
		{"-cmd", "'-cmd"},
		{"@cmd", "'@cmd"},
		{"\tcmd", "'\tcmd"},
		{"\rcmd", "'\rcmd"},
		{"normal text", "normal text"},
		{"123", "123"},
	}
	for _, tt := range tests {
		got := csvSanitize(tt.input)
		if got != tt.want {
			t.Errorf("csvSanitize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
