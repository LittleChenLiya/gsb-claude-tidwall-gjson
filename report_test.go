package gjson

import (
	"fmt"
	"strings"
	"testing"
)

// TestQueryReportArrayFilter is the primary acceptance case: the filtered
// multi-result query reports both hits with their real array indexes and
// their exact ranges in the original JSON.
func TestQueryReportArrayFilter(t *testing.T) {
	json := `{"users":[{"name":"Ann","active":true},{"name":"Bob","active":false},{"name":"Cid","active":true}]}`
	rep := Query(json, "users.#(active==true)#.name")
	if len(rep.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(rep.Entries), rep.Entries)
	}
	want := []struct {
		path       string
		start, end int
		value      string
	}{
		{"users.0.name", 18, 23, "Ann"},
		{"users.2.name", 77, 82, "Cid"},
	}
	for i, w := range want {
		e := rep.Entries[i]
		if e.Kind != EntryHit {
			t.Fatalf("entry %d: expected Hit, got %s", i, e.Kind)
		}
		if e.Path != w.path {
			t.Fatalf("entry %d: expected path %q, got %q", i, w.path, e.Path)
		}
		if e.Start != w.start || e.End != w.end {
			t.Fatalf("entry %d: expected range [%d,%d), got [%d,%d)",
				i, w.start, w.end, e.Start, e.End)
		}
		if json[e.Start:e.End] != e.Result.Raw {
			t.Fatalf("entry %d: range does not match raw %q", i, e.Result.Raw)
		}
		if e.Result.Str != w.value {
			t.Fatalf("entry %d: expected %q, got %q", i, w.value, e.Result.Str)
		}
		// path re-query: the reported path resolves back to the same value
		if got := Get(json, e.Path); got.Str != w.value {
			t.Fatalf("entry %d: re-query of %q returned %q", i, e.Path, got.Str)
		}
	}
}

// TestQueryReportEscapedKeys verifies that a key containing a dot reports
// a different path than ordinary nesting, and that dots, quotes and
// backslashes are escaped so the path queries back to the same value.
func TestQueryReportEscapedKeys(t *testing.T) {
	json := `{"a.b":1,"a":{"b":2}}`
	flat := Query(json, `a\.b`)
	if len(flat.Entries) != 1 || flat.Entries[0].Path != `a\.b` {
		t.Fatalf("flat key: unexpected entries %+v", flat.Entries)
	}
	nested := Query(json, "a.b")
	if len(nested.Entries) != 1 || nested.Entries[0].Path != "a.b" {
		t.Fatalf("nested key: unexpected entries %+v", nested.Entries)
	}
	if flat.Entries[0].Path == nested.Entries[0].Path {
		t.Fatal("flat key and nested key must report different paths")
	}
	if got := Get(json, flat.Entries[0].Path); got.Int() != 1 {
		t.Fatalf("re-query of flat path returned %v", got)
	}
	if got := Get(json, nested.Entries[0].Path); got.Int() != 2 {
		t.Fatalf("re-query of nested path returned %v", got)
	}

	// keys containing dots, quotes and backslashes
	bs := string(rune(92)) // backslash
	key := `we` + bs + `ird."key`
	json = `{"root":{` + string(AppendJSONString(nil, key)) + `:42}}`
	rep := Query(json, "root."+Escape(key))
	if len(rep.Entries) != 1 || rep.Entries[0].Kind != EntryHit {
		t.Fatalf("unexpected entries %+v", rep.Entries)
	}
	wantPath := "root." + Escape(key)
	if rep.Entries[0].Path != wantPath {
		t.Fatalf("expected path %q, got %q", wantPath, rep.Entries[0].Path)
	}
	if got := Get(json, rep.Entries[0].Path); got.Int() != 42 {
		t.Fatalf("re-query of escaped path returned %v", got)
	}
}

// TestQueryReportDerived verifies that values synthesized by modifiers or
// calculations are reported as derived, with no fabricated source index.
func TestQueryReportDerived(t *testing.T) {
	json := `{"users":[{"name":"Ann"}],"age":30}`
	for _, path := range []string{"@keys", "users.#", "{age,users.#.name}"} {
		rep := Query(json, path)
		if len(rep.Entries) != 1 {
			t.Fatalf("%s: expected 1 entry, got %+v", path, rep.Entries)
		}
		e := rep.Entries[0]
		if e.Kind != EntryDerived {
			t.Fatalf("%s: expected Derived, got %s", path, e.Kind)
		}
		if e.Start != -1 || e.End != -1 {
			t.Fatalf("%s: derived entry must not fabricate an index, got [%d,%d)",
				path, e.Start, e.End)
		}
		if e.Path != "" {
			t.Fatalf("%s: derived entry must not have a source path, got %q",
				path, e.Path)
		}
	}
	// the modifier value itself is still reported
	if got := Query(json, "@keys").Entries[0].Result; got.Str != "users" {
		// @keys returns an array of the object keys
		if got.Raw != `["users","age"]` {
			t.Fatalf("unexpected @keys result %v", got)
		}
	}
}

// TestQueryReportMissingVsNull verifies that a missing path and an
// explicit JSON null are reported separately.
func TestQueryReportMissingVsNull(t *testing.T) {
	json := `{"a":null,"b":1}`
	rep := Query(json, "a")
	if len(rep.Entries) != 1 || rep.Entries[0].Kind != EntryNull {
		t.Fatalf("expected a single Null entry, got %+v", rep.Entries)
	}
	e := rep.Entries[0]
	if e.Start != 5 || e.End != 9 || json[e.Start:e.End] != "null" {
		t.Fatalf("null entry range: got [%d,%d)", e.Start, e.End)
	}
	if e.Path != "a" {
		t.Fatalf("null entry path: got %q", e.Path)
	}

	rep = Query(json, "c")
	if len(rep.Entries) != 1 || rep.Entries[0].Kind != EntryMissing {
		t.Fatalf("expected a single Missing entry, got %+v", rep.Entries)
	}
	if rep.Entries[0].Start != -1 || rep.Entries[0].End != -1 {
		t.Fatal("missing entry must not have a source range")
	}
}

// TestQueryReportNullFill verifies the null-filling semantics of
// projections: array elements skipped because the tail path does not
// exist are reported as Missing entries with their actual array index,
// while explicit JSON nulls are reported as Null entries.
func TestQueryReportNullFill(t *testing.T) {
	json := `{"users":[{"name":"a"},{"x":1},{"name":null}]}`
	rep := Query(json, "users.#.name")
	if len(rep.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %+v", rep.Entries)
	}
	kinds := []EntryKind{EntryHit, EntryMissing, EntryNull}
	paths := []string{"users.0.name", "users.1.name", "users.2.name"}
	for i := range kinds {
		e := rep.Entries[i]
		if e.Kind != kinds[i] || e.Path != paths[i] {
			t.Fatalf("entry %d: expected %s at %q, got %s at %q",
				i, kinds[i], paths[i], e.Kind, e.Path)
		}
	}
	// the null entry points at the null literal in the original JSON
	if e := rep.Entries[2]; json[e.Start:e.End] != "null" {
		t.Fatalf("null entry range %q", json[e.Start:e.End])
	}
	// the missing entry has no source range
	if e := rep.Entries[1]; e.Start != -1 || e.End != -1 {
		t.Fatal("missing entry must not have a source range")
	}

	// filtered queries: elements excluded by the filter are not reported,
	// but matching elements that lack the tail are null-filled.
	json = `{"users":[{"name":"a","active":true},{"active":true},{"name":"b","active":false}]}`
	rep = Query(json, "users.#(active==true)#.name")
	if len(rep.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %+v", rep.Entries)
	}
	if rep.Entries[0].Kind != EntryHit || rep.Entries[0].Path != "users.0.name" {
		t.Fatalf("unexpected first entry %+v", rep.Entries[0])
	}
	if rep.Entries[1].Kind != EntryMissing || rep.Entries[1].Path != "users.1.name" {
		t.Fatalf("unexpected second entry %+v", rep.Entries[1])
	}
}

// TestQueryReportDuplicateKeys verifies the duplicate object key
// semantics: a reported path resolves to the first occurrence when
// re-queried, and entries located under a repeated key are flagged.
func TestQueryReportDuplicateKeys(t *testing.T) {
	json := `{"a":1,"a":2}`
	rep := Query(json, "a")
	if len(rep.Entries) != 1 || rep.Entries[0].Kind != EntryHit {
		t.Fatalf("unexpected entries %+v", rep.Entries)
	}
	if rep.Entries[0].Result.Int() != 1 {
		t.Fatal("Get semantics resolve a repeated key to the first occurrence")
	}
	if rep.Entries[0].Duplicate {
		t.Fatal("the first occurrence is not a duplicate")
	}

	// an entry located at the second occurrence is flagged as a duplicate;
	// re-querying its path returns the first occurrence instead.
	second := strings.Index(json, ":2") + 1
	path, dup, ok := findPath(json, second)
	if !ok || path != "a" || !dup {
		t.Fatalf("findPath second occurrence: path=%q dup=%v ok=%v", path, dup, ok)
	}
	if got := Get(json, path); got.Int() != 1 {
		t.Fatalf("documented semantics: re-query of %q returns first occurrence, got %v",
			path, got)
	}
}

// TestQueryReportRoundTrip verifies that every reported hit or null path
// queries back to the same original value.
func TestQueryReportRoundTrip(t *testing.T) {
	json := `{
		"users": [
			{"name": "Ann", "tags": ["a", "b"], "nick": null},
			{"name": "Bob", "tags": []},
			{"na.me": "x", "q\"u": {"esc": true}}
		],
		"a.b": {"c": [10, 20, {"d.e": "f"}]}
	}`
	paths := []string{
		"users",
		"users.0",
		"users.0.name",
		"users.0.tags",
		"users.0.tags.1",
		"users.0.nick",
		"users.#.name",
		"users.#.tags",
		`users.#(name=="Bob")#.tags`,
		`a\.b.c`,
		`a\.b.c.2.d\.e`,
	}
	for _, path := range paths {
		rep := Query(json, path)
		if len(rep.Entries) == 0 {
			t.Fatalf("%s: no entries", path)
		}
		for _, e := range rep.Entries {
			switch e.Kind {
			case EntryHit, EntryNull:
				if e.Start < 0 || e.End > len(json) || e.Start >= e.End {
					t.Fatalf("%s: bad range [%d,%d)", path, e.Start, e.End)
				}
				if json[e.Start:e.End] != e.Result.Raw {
					t.Fatalf("%s: range %q != raw %q",
						path, json[e.Start:e.End], e.Result.Raw)
				}
				got := Get(json, e.Path)
				if !got.Exists() || got.Raw != e.Result.Raw {
					t.Fatalf("%s: re-query of %q returned %q, want %q",
						path, e.Path, got.Raw, e.Result.Raw)
				}
			case EntryDerived:
				if e.Start != -1 || e.End != -1 || e.Path != "" {
					t.Fatalf("%s: derived entry carries a source position", path)
				}
			}
		}
	}
}

// TestQueryReportTopLevelArray verifies projections over a root array.
func TestQueryReportTopLevelArray(t *testing.T) {
	json := `[{"name":"Ann"},{"x":1},{"name":"Cid"}]`
	rep := Query(json, "#.name")
	if len(rep.Entries) != 3 {
		t.Fatalf("expected 3 entries, got %+v", rep.Entries)
	}
	if rep.Entries[0].Path != "0.name" || rep.Entries[0].Kind != EntryHit {
		t.Fatalf("unexpected entry %+v", rep.Entries[0])
	}
	if rep.Entries[1].Path != "1.name" || rep.Entries[1].Kind != EntryMissing {
		t.Fatalf("unexpected entry %+v", rep.Entries[1])
	}
	if rep.Entries[2].Path != "2.name" || rep.Entries[2].Kind != EntryHit {
		t.Fatalf("unexpected entry %+v", rep.Entries[2])
	}
	for _, e := range rep.Entries {
		if e.Kind == EntryHit {
			if got := Get(json, e.Path); got.Raw != e.Result.Raw {
				t.Fatalf("re-query of %q returned %q", e.Path, got.Raw)
			}
		}
	}
}

// ExampleQuery is a short demo of the two-hit filtered query report.
func ExampleQuery() {
	json := `{"users":[{"name":"Ann","active":true},{"name":"Bob","active":false},{"name":"Cid","active":true}]}`
	report := Query(json, "users.#(active==true)#.name")
	for _, e := range report.Entries {
		fmt.Printf("%s %s [%d,%d) %s\n", e.Path, e.Kind, e.Start, e.End, e.Result.Raw)
	}
	// Output:
	// users.0.name Hit [18,23) "Ann"
	// users.2.name Hit [77,82) "Cid"
}
