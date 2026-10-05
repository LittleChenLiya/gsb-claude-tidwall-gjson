package gjson

import (
	"fmt"
	"strings"
	"testing"
)

// projectionFixture keeps the two active users on purpose: the second one
// has no "name" key at all, while an explicit JSON null is exercised on a
// third active record so that "missing" and "null" cannot be conflated.
const projectionFixture = `{
  "users": [
    {"id": 1, "name": "alice", "active": true},
    {"id": 2, "active": true},
    {"id": 3, "name": null, "active": true},
    {"id": 4, "name": "erin", "active": false}
  ]
}`

// plan returns the explicitly registered projection used by the tests.
// Columns are registered id-first, then name, then a derived column.
func plan() Projection {
	return Projection{
		Path:  "users",
		Where: "active==true",
		Columns: []ProjectionColumn{
			{Name: "id", Field: "id"},
			{Name: "name", Field: "name"},
			{Name: "name_upper", Field: "name", Modifier: "upper"},
		},
	}
}

func init() {
	// A small custom modifier used to prove derived columns carry their
	// source and do not borrow an original-text position.
	AddModifier("upper", func(json, arg string) string {
		r := Parse(json)
		if r.Type != String {
			return "null"
		}
		b := make([]byte, 0, len(r.Str)+2)
		b = append(b, '"')
		b = append(b, strings.ToUpper(r.Str)...)
		b = append(b, '"')
		return string(b)
	})
	// A modifier that always fails; failed fields must keep the record index.
	AddModifier("boom", func(json, arg string) string {
		panic("boom")
	})
}

// TestProjectionAcceptance covers the stated acceptance requirements:
// one of two users missing name, id and name still bound to the same user,
// active filtering preserving original ordinal indexes.
func TestProjectionAcceptance(t *testing.T) {
	p := plan()
	rows, err := p.Run(projectionFixture)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// active==true keeps records 0,1,2; record 3 (erin, inactive) is out.
	if len(rows) != 3 {
		t.Fatalf("want 3 matched rows, got %d: %s", len(rows), p.JSON(rows))
	}

	// Original array ordinals are preserved after filtering: rows are
	// records 0, 1, 2, never renumbered.
	wantIdx := []int{0, 1, 2}
	for i, row := range rows {
		if row.RecordIndex != wantIdx[i] {
			t.Errorf("row %d RecordIndex: want %d, got %d",
				i, wantIdx[i], row.RecordIndex)
		}
	}

	// id is bound to the same record as name: check correspondence and that
	// both cells anchor to the SAME record byte offset.
	wantID := []string{"1", "2", "3"}
	for i, row := range rows {
		id := row.Cells[0]
		name := row.Cells[1]
		if id.Value.String() != wantID[i] {
			t.Errorf("row %d id: want %s, got %s",
				i, wantID[i], id.Value.String())
		}
		// A present id and the name cell belong to the same matched record:
		// the id absolute offset must lie inside this row record span, and a
		// missing name keeps the record offset itself.
		if name.Missing {
			if name.Value.Index != row.RecordOffset {
				t.Errorf("row %d missing name index %d != record offset %d",
					i, name.Value.Index, row.RecordOffset)
			}
		} else if name.Value.Index < row.RecordOffset {
			t.Errorf("row %d name offset %d outside record at %d",
				i, name.Value.Index, row.RecordOffset)
		}
	}

	// Row 0: alice has a name.
	if !rows[0].Cells[1].Value.Exists() ||
		rows[0].Cells[1].Value.String() != "alice" {
		t.Fatalf("row 0 name: want alice, got %+v", rows[0].Cells[1])
	}

	// Row 1: name key is absent. Missing must be true and distinct from null.
	missing := rows[1].Cells[1]
	if !missing.Missing {
		t.Errorf("row 1 name: want Missing=true, got false")
	}
	if missing.Value.Exists() {
		t.Errorf("row 1 name: missing value must not exist, raw=%q",
			missing.Value.Raw)
	}
	if missing.Value.Type != Null || missing.Value.Raw != "" {
		t.Errorf("row 1 name: want zero Null result, got type=%v raw=%q",
			missing.Value.Type, missing.Value.Raw)
	}

	// Row 2: name is an explicit null. Exists must be true, Missing false.
	nullname := rows[2].Cells[1]
	if nullname.Missing {
		t.Errorf("row 2 name: explicit null must not be Missing")
	}
	if !nullname.Value.Exists() || nullname.Value.Raw != "null" {
		t.Errorf("row 2 name: want explicit null, got raw=%q",
			nullname.Value.Raw)
	}
}

// TestProjectionMissingKeepsRecordIndex verifies that missing and failed
// cells retain the owning record byte offset rather than zero.
func TestProjectionMissingKeepsRecordIndex(t *testing.T) {
	p := Projection{
		Path:  "users",
		Where: "active==true",
		Columns: []ProjectionColumn{
			{Name: "id", Field: "id"},
			{Name: "nick", Field: "nick"},
			{Name: "nick_d", Field: "nick", Modifier: "upper"},
			{Name: "boom_d", Field: "id", Modifier: "boom"},
		},
	}
	rows, err := p.Run(projectionFixture)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, row := range rows {
		miss := row.Cells[1]
		if !miss.Missing {
			t.Fatalf("record %d: nick should be missing", row.RecordIndex)
		}
		if miss.Value.Index != row.RecordOffset {
			t.Errorf("record %d: missing cell index %d != record offset %d",
				row.RecordIndex, miss.Value.Index, row.RecordOffset)
		}
		// Derivation over a missing field stays source-tagged and must not
		// manufacture a source position; it still reports Missing.
		missDerived := row.Cells[2]
		if !missDerived.Derived || missDerived.Source != "upper" {
			t.Errorf("record %d: derived-missing not source-tagged: %+v",
				row.RecordIndex, missDerived)
		}
		if !missDerived.Missing {
			t.Errorf("record %d: derived-missing Missing=false",
				row.RecordIndex)
		}
		if missDerived.Value.Index != row.RecordOffset {
			t.Errorf("record %d: derived-missing index %d != record %d",
				row.RecordIndex, missDerived.Value.Index, row.RecordOffset)
		}
		// Failed modifier keeps the record index and flags Failed/Derived.
		boom := row.Cells[3]
		if !boom.Derived || !boom.Failed {
			t.Errorf("record %d: boom cell Derived=%v Failed=%v",
				row.RecordIndex, boom.Derived, boom.Failed)
		}
		if boom.Value.Index != row.RecordOffset {
			t.Errorf("record %d: failed cell index %d != record offset %d",
				row.RecordIndex, boom.Value.Index, row.RecordOffset)
		}
	}
}

// TestProjectionDerivedSource verifies modifier-produced values carry their
// source name and a cleared source position.
func TestProjectionDerivedSource(t *testing.T) {
	rows, err := plan().Run(projectionFixture)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	derived := rows[0].Cells[2]
	if !derived.Derived || derived.Source != "upper" {
		t.Fatalf("derived cell: Derived=%v Source=%q",
			derived.Derived, derived.Source)
	}
	if derived.Value.String() != "ALICE" {
		t.Fatalf("derived value: want ALICE, got %q", derived.Value.String())
	}
	// Derived output must not claim a position inside the original text.
	if derived.Value.Index != 0 {
		t.Errorf("derived value must not fake an original index, got %d",
			derived.Value.Index)
	}
}

// TestProjectionNoIndependentStitch is the anti-pattern guard: id and name
// must come from the same matched record. This is tested by registering
// fields whose filtered lists would differ and asserting row alignment.
func TestProjectionNoIndependentStitch(t *testing.T) {
	// Independent filtering would produce ids [1,2,3] and names
	// [alice,null] (existing-name users only), which stitched by position
	// pairs id 2 with null wrongly and drops id 3. A single-pass record
	// binding pairs every id with that same record name (missing/null).
	p := Projection{
		Path:  "users",
		Where: "active==true",
		Columns: []ProjectionColumn{
			{Name: "id", Field: "id"},
			{Name: "name", Field: "name"},
		},
	}
	rows, err := p.Run(projectionFixture)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	wantID := []string{"1", "2", "3"}
	wantMissing := []bool{false, true, false}
	wantNull := []bool{false, false, true}
	for i, row := range rows {
		if row.Cells[0].Value.String() != wantID[i] {
			t.Fatalf("row %d id mismatch: %s", i, row.Cells[0].Value.Raw)
		}
		if row.Cells[1].Missing != wantMissing[i] {
			t.Errorf("row %d Missing: want %v", i, wantMissing[i])
		}
		isNull := !row.Cells[1].Missing &&
			row.Cells[1].Value.Raw == "null"
		if isNull != wantNull[i] {
			t.Errorf("row %d explicit-null: want %v", i, wantNull[i])
		}
	}
}

// TestProjectionColumnOrder asserts output columns follow the registration
// list even when field order differs from the JSON document order.
func TestProjectionColumnOrder(t *testing.T) {
	p := Projection{
		Path:  "users",
		Where: "id==1",
		Columns: []ProjectionColumn{
			{Name: "zeta", Field: "active"},
			{Name: "alpha", Field: "id"},
			{Name: "mid", Field: "name"},
		},
	}
	rows, err := p.Run(projectionFixture)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	out := p.JSON(rows)
	want := `[{"zeta":true,"alpha":1,"mid":"alice"}]`
	if out != want {
		t.Fatalf("column order:\nwant %s\ngot  %s", want, out)
	}
	if names := p.ColumnNames(); len(names) != 3 ||
		names[0] != "zeta" || names[1] != "alpha" || names[2] != "mid" {
		t.Fatalf("ColumnNames order wrong: %v", names)
	}
}

// TestProjectionJSONDoesNotMutateSource verifies the original document is
// left unchanged after running and rendering a projection.
func TestProjectionJSONDoesNotMutateSource(t *testing.T) {
	src := projectionFixture
	p := plan()
	rows, err := p.Run(src)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	_ = p.JSON(rows)
	if src != projectionFixture {
		t.Fatalf("source document mutated")
	}
}

// TestProjectionJSONExample renders the two projection examples asked for:
// the acceptance fixture and an object-column derivation example.
func TestProjectionJSONExample(t *testing.T) {
	p := plan()
	rows, err := p.Run(projectionFixture)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := p.JSON(rows)
	want := `[{"id":1,"name":"alice","name_upper":"ALICE"},` +
		`{"id":2,"name":null,"name_upper":null},` +
		`{"id":3,"name":null,"name_upper":null}]`
	if out != want {
		t.Fatalf("projection example:\nwant %s\ngot  %s", want, out)
	}
}

// ExampleProjection documents the two projection lines on one document:
// a direct field projection (id, name) and a derived one (name_upper)
// produced by the "upper" modifier, bound to the same matched records.
func ExampleProjection() {
	const doc = `{"users":[{"id":1,"name":"alice","active":true},` +
		`{"id":2,"active":true}]}`

	p := Projection{
		Path:  "users",
		Where: "active==true",
		Columns: []ProjectionColumn{
			{Name: "id", Field: "id"},
			{Name: "name", Field: "name"},
			{Name: "name_upper", Field: "name", Modifier: "upper"},
		},
	}
	rows, _ := p.Run(doc)
	// direct fields, registration order:
	fmt.Println(p.JSON(rows[:1]))
	// the same records including the derived column and a missing name:
	fmt.Println(p.JSON(rows[1:]))
	// Output:
	// [{"id":1,"name":"alice","name_upper":"ALICE"}]
	// [{"id":2,"name":null,"name_upper":null}]
}
