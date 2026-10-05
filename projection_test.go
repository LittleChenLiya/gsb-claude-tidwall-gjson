// Copyright 2024 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

// Directed test command:
//
//	go test -run 'TestProjection|ExampleProjection' -v .
package gjson

import (
	"fmt"
	"testing"
)

// The acceptance fixture: two users, one missing name. The naive approach of
// filtering each field independently and zipping by position would pair id 1
// with name "Bob". A projection binds every field to the same record.
const projectionJSON = `{
  "users": [
    {"id": 1, "note": null, "tags": ["a", "b"], "active": true},
    {"id": 2, "name": "Bob", "note": "hi", "tags": ["c"], "active": false}
  ]
}`

func TestProjectionRun(t *testing.T) {
	proj := NewProjection().
		Add("id", "id").
		Add("name", "name").
		Add("note", "note").
		Add("rtags", "tags|@reverse")

	// Filter active users. The query runs once per real record; the original
	// index of each matched record is preserved.
	recs := proj.Run(projectionJSON, "users.#(active==true)")
	if len(recs) != 1 {
		t.Fatalf("expected 1 matched record, got %d", len(recs))
	}
	rec := recs[0]
	if rec.Index != 0 {
		t.Fatalf("expected original index 0, got %d", rec.Index)
	}
	if len(rec.Values) != 4 {
		t.Fatalf("expected 4 columns in registration order, got %d", len(rec.Values))
	}

	// Column order follows the registration list.
	wantNames := []string{"id", "name", "note", "rtags"}
	for i, w := range wantNames {
		if rec.Values[i].Name != w {
			t.Fatalf("column %d: expected %q, got %q", i, w, rec.Values[i].Name)
		}
	}

	// id is a direct value at a real source position.
	id := rec.Values[0]
	if !id.Exists() || id.Int() != 1 {
		t.Fatalf("expected id 1, got exists=%v int=%d", id.Exists(), id.Int())
	}
	if id.Index <= 0 || !isSourceValue(projectionJSON, id.Result) {
		t.Fatalf("id should be a direct source value, index=%d", id.Index)
	}

	// name is missing (distinct from null): it does not exist, yet it is still
	// bound to record 0, not paired with user 2's name.
	name := rec.Values[1]
	if name.Exists() {
		t.Fatalf("expected name to be missing, got %q", name.String())
	}
	if name.Index != rec.Index {
		t.Fatalf("missing name should preserve record index %d, got %d", rec.Index, name.Index)
	}

	// note is an explicit JSON null: it exists but is Null.
	note := rec.Values[2]
	if !note.Exists() || note.Type != Null {
		t.Fatalf("expected explicit null, got exists=%v type=%s", note.Exists(), note.Type)
	}
	if note.Index <= 0 || !isSourceValue(projectionJSON, note.Result) {
		t.Fatalf("null note should be a direct source value, index=%d", note.Index)
	}

	// rtags is modifier-derived: it must not forge a source position.
	rtags := rec.Values[3]
	if !rtags.Derived {
		t.Fatal("expected rtags to be marked derived")
	}
	if rtags.Index != 0 {
		t.Fatalf("derived value must not claim a source position, got index=%d", rtags.Index)
	}
	if got := rtags.String(); got != `["b","a"]` {
		t.Fatalf("expected derived [\"b\",\"a\"], got %q", got)
	}

	// The source document is unchanged.
	if !Valid(projectionJSON) {
		t.Fatal("source document was modified")
	}
}

func TestProjectionAllRecords(t *testing.T) {
	proj := NewProjection().Add("id", "id").Add("name", "name")
	recs := proj.Run(projectionJSON, "users")
	if len(recs) != 2 {
		t.Fatalf("expected 2 records, got %d", len(recs))
	}
	// Original indexes are preserved in source order.
	if recs[0].Index != 0 || recs[1].Index != 1 {
		t.Fatalf("expected indexes 0,1, got %d,%d", recs[0].Index, recs[1].Index)
	}
	// id and name correspond to the same record: user 1 has no name, user 2
	// has name "Bob".
	if recs[0].Values[0].Int() != 1 || recs[0].Values[1].Exists() {
		t.Fatal("record 0 should be id 1 with missing name")
	}
	if recs[1].Values[0].Int() != 2 || recs[1].Values[1].String() != "Bob" {
		t.Fatal("record 1 should be id 2 with name Bob")
	}
}

func TestProjectionMissingArray(t *testing.T) {
	proj := NewProjection().Add("id", "id")
	if recs := proj.Run(projectionJSON, "nope"); recs != nil {
		t.Fatalf("expected nil for missing array, got %v", recs)
	}
}

// Two lines of projection examples.
func ExampleProjection() {
	proj := NewProjection().Add("id", "id").Add("name", "name")
	for _, rec := range proj.Run(projectionJSON, "users.#(active==true)") {
		fmt.Printf("id=%d nameExists=%v\n", rec.Values[0].Int(), rec.Values[1].Exists())
	}
	// Output: id=1 nameExists=false
}

func ExampleProjection_Run() {
	proj := NewProjection().Add("id", "id").Add("name", "name")
	for _, rec := range proj.Run(projectionJSON, "users") {
		fmt.Printf("index=%d id=%d name=%q\n", rec.Index, rec.Values[0].Int(), rec.Values[1].String())
	}
	// Output:
	// index=0 id=1 name=""
	// index=1 id=2 name="Bob"
}
