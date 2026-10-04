package gjson

import (
	"testing"
)

func TestReportFilter(t *testing.T) {
	json := `{"users":[{"name":"Alice","active":true},{"name":"Bob","active":false},{"name":"Carol","active":true}]}`
	hits := Report(json, "users.#(active==true)#.name")
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	expected := []struct {
		path  string
		index int
		end   int
		raw   string
	}{
		{"users.0.name", 18, 25, `"Alice"`},
		{"users.2.name", 79, 86, `"Carol"`},
	}
	for i, e := range expected {
		h := hits[i]
		if h.Path != e.path {
			t.Errorf("hit %d: path = %q, want %q", i, h.Path, e.path)
		}
		if h.Index != e.index {
			t.Errorf("hit %d: index = %d, want %d", i, h.Index, e.index)
		}
		if h.End != e.end {
			t.Errorf("hit %d: end = %d, want %d", i, h.End, e.end)
		}
		if h.Result.Raw != e.raw {
			t.Errorf("hit %d: raw = %q, want %q", i, h.Result.Raw, e.raw)
		}
		if h.Missing {
			t.Errorf("hit %d: missing = true, want false", i)
		}
		if h.Derived {
			t.Errorf("hit %d: derived = true, want false", i)
		}
		// Round-trip check
		rt := Get(json, h.Path)
		if rt.Raw != h.Result.Raw {
			t.Errorf("hit %d: round-trip raw = %q, want %q", i, rt.Raw, h.Result.Raw)
		}
	}
}

func TestReportDottedKey(t *testing.T) {
	// A key with a dot must produce a different path than nested keys.
	json := `{"a.b":"dotted","a":{"b":"nested"}}`
	hits := Report(json, `a\.b`)
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].Path != `a\.b` {
		t.Errorf("dotted key path = %q, want %q", hits[0].Path, `a\.b`)
	}
	if hits[0].Result.Raw != `"dotted"` {
		t.Errorf("dotted key raw = %q, want %q", hits[0].Result.Raw, `"dotted"`)
	}
	// Round-trip
	rt := Get(json, hits[0].Path)
	if rt.Raw != hits[0].Result.Raw {
		t.Errorf("round-trip raw = %q, want %q", rt.Raw, hits[0].Result.Raw)
	}

	// Nested key
	hitsN := Report(json, "a.b")
	if len(hitsN) != 1 {
		t.Fatalf("expected 1 nested hit, got %d", len(hitsN))
	}
	if hitsN[0].Path != "a.b" {
		t.Errorf("nested key path = %q, want %q", hitsN[0].Path, "a.b")
	}
	if hitsN[0].Result.Raw != `"nested"` {
		t.Errorf("nested key raw = %q, want %q", hitsN[0].Result.Raw, `"nested"`)
	}
}

func TestReportSpecialChars(t *testing.T) {
	json := `{"quote\"key":"val","dot.key":"val2"}`
	tests := []struct {
		key  string
		path string
		raw  string
	}{
		{`quote"key`, `quote\"key`, `"val"`},
		{`dot.key`, `dot\.key`, `"val2"`},
	}
	for _, tc := range tests {
		hits := Report(json, Escape(tc.key))
		if len(hits) != 1 {
			t.Fatalf("key %q: expected 1 hit, got %d", tc.key, len(hits))
		}
		h := hits[0]
		if h.Path != tc.path {
			t.Errorf("key %q: path = %q, want %q", tc.key, h.Path, tc.path)
		}
		if h.Result.Raw != tc.raw {
			t.Errorf("key %q: raw = %q, want %q", tc.key, h.Result.Raw, tc.raw)
		}
		// Round-trip
		rt := Get(json, h.Path)
		if rt.Raw != h.Result.Raw {
			t.Errorf("key %q: round-trip raw = %q, want %q", tc.key, rt.Raw, h.Result.Raw)
		}
	}
}

func TestReportNullVsMissing(t *testing.T) {
	json := `{"items":[{"name":"Alice","extra":null},{"name":"Bob"}]}`
	hits := Report(json, "items.#.extra")
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	// First element has null
	if hits[0].Missing {
		t.Errorf("first hit: missing = true, want false")
	}
	if hits[0].Result.Type != Null {
		t.Errorf("first hit: type = %v, want Null", hits[0].Result.Type)
	}
	if hits[0].Result.Raw != "null" {
		t.Errorf("first hit: raw = %q, want %q", hits[0].Result.Raw, "null")
	}
	// Second element is missing
	if !hits[1].Missing {
		t.Errorf("second hit: missing = false, want true")
	}
	if hits[1].Path != "items.1.extra" {
		t.Errorf("second hit: path = %q, want %q", hits[1].Path, "items.1.extra")
	}
}

func TestReportDuplicateKeys(t *testing.T) {
	json := `{"a":1,"a":2}`
	hits := Report(json, "a")
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].Result.Raw != "1" {
		t.Errorf("raw = %q, want %q", hits[0].Result.Raw, "1")
	}
	// Should match Get behavior
	rt := Get(json, "a")
	if rt.Raw != hits[0].Result.Raw {
		t.Errorf("Get raw = %q, want %q", rt.Raw, hits[0].Result.Raw)
	}
}

func TestReportModifierDerived(t *testing.T) {
	json := `{"name":"Alice","age":30}`
	for _, path := range []string{"@keys", "@values", "@tostr", "@keys.0"} {
		hits := Report(json, path)
		if len(hits) != 1 {
			t.Fatalf("path %q: expected 1 hit, got %d", path, len(hits))
		}
		h := hits[0]
		if !h.Derived {
			t.Errorf("path %q: derived = false, want true", path)
		}
		if h.Index != -1 {
			t.Errorf("path %q: index = %d, want -1", path, h.Index)
		}
		if h.End != -1 {
			t.Errorf("path %q: end = %d, want -1", path, h.End)
		}
		// Round-trip
		rt := Get(json, h.Path)
		if rt.Raw != h.Result.Raw {
			t.Errorf("path %q: round-trip raw = %q, want %q", path, rt.Raw, h.Result.Raw)
		}
	}
}

func TestReportWildcard(t *testing.T) {
	json := `{"a":1,"b":2,"c":3}`
	hits := Report(json, "*")
	if len(hits) != 3 {
		t.Fatalf("expected 3 hits, got %d", len(hits))
	}
	expected := []string{"a", "b", "c"}
	for i, e := range expected {
		if hits[i].Path != e {
			t.Errorf("hit %d: path = %q, want %q", i, hits[i].Path, e)
		}
	}
}

func TestReportArrayIndex(t *testing.T) {
	json := `[10,20,30]`
	hits := Report(json, "1")
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].Path != "1" {
		t.Errorf("path = %q, want %q", hits[0].Path, "1")
	}
	if hits[0].Index != 3 {
		t.Errorf("index = %d, want 3", hits[0].Index)
	}
	if hits[0].Result.Raw != "20" {
		t.Errorf("raw = %q, want %q", hits[0].Result.Raw, "20")
	}
}

func TestReportEmpty(t *testing.T) {
	hits := Report(`{"a":1}`, "")
	if len(hits) != 0 {
		t.Errorf("expected 0 hits for empty path, got %d", len(hits))
	}
	hits = Report(`{"a":1}`, "b")
	if len(hits) != 0 {
		t.Errorf("expected 0 hits for missing key, got %d", len(hits))
	}
}
