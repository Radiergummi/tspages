package id

import (
	"strings"
	"testing"
)

func TestNew_Format(t *testing.T) {
	got := New("sub")
	if !strings.HasPrefix(got, "sub_") {
		t.Fatalf("expected sub_ prefix, got %q", got)
	}
	random := strings.TrimPrefix(got, "sub_")
	if len(random) != idLen {
		t.Fatalf("expected %d random chars, got %d in %q", idLen, len(random), got)
	}
	for _, c := range random {
		if !strings.ContainsRune(alphabet, c) {
			t.Errorf("character %q not in base58 alphabet", c)
		}
	}
}

func TestNew_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for range 1000 {
		id := New("test")
		if seen[id] {
			t.Fatalf("duplicate ID: %s", id)
		}
		seen[id] = true
	}
}

func TestNew_DifferentPrefixes(t *testing.T) {
	dep := New("dep")
	sub := New("sub")
	msg := New("msg")

	if !strings.HasPrefix(dep, "dep_") {
		t.Errorf("dep prefix missing: %s", dep)
	}
	if !strings.HasPrefix(sub, "sub_") {
		t.Errorf("sub prefix missing: %s", sub)
	}
	if !strings.HasPrefix(msg, "msg_") {
		t.Errorf("msg prefix missing: %s", msg)
	}
}
