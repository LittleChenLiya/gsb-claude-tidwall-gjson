// Copyright 2024 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package gjson

import "testing"

// TestGetStrictSyntaxError covers the first required input:
// {"a":1}garbage querying "a" must report a bad input before any query.
func TestGetStrictSyntaxError(t *testing.T) {
	res := GetStrict(`{"a":1}garbage`, "a")
	if res.Valid {
		t.Fatal("expected invalid input")
	}
	if res.Pos != 7 {
		t.Fatalf("expected first error position 7, got %d", res.Pos)
	}
	if res.Exists() {
		t.Fatal("expected no query result on invalid input")
	}
	err := res.Err()
	if err == nil {
		t.Fatal("expected an error")
	}
	se, ok := err.(*SyntaxError)
	if !ok {
		t.Fatalf("expected *SyntaxError, got %T", err)
	}
	if se.Pos != 7 {
		t.Fatalf("expected error position 7, got %d", se.Pos)
	}
	if msg := err.Error(); msg == "" {
		t.Fatal("expected a non-empty error message")
	}
}

// TestGetStrictNullExists covers the second required input:
// a valid {"a":null} must report the present null as existing.
func TestGetStrictNullExists(t *testing.T) {
	res := GetStrict(`{"a":null}`, "a")
	if !res.Valid {
		t.Fatal("expected valid input")
	}
	if !res.Exists() {
		t.Fatal("expected present null to exist")
	}
	if res.Type != Null {
		t.Fatalf("expected Null type, got %v", res.Type)
	}
	if res.Raw != "null" {
		t.Fatalf("expected raw %q, got %q", "null", res.Raw)
	}
	if res.Pos != 0 {
		t.Fatalf("expected zero position for valid input, got %d", res.Pos)
	}
	if res.Err() != nil {
		t.Fatal("expected no error for valid input")
	}
}

// TestGetStrictMissingPath covers the third required input:
// a valid input with a missing path must be reported as a legal missing path.
func TestGetStrictMissingPath(t *testing.T) {
	res := GetStrict(`{"a":1}`, "b")
	if !res.Valid {
		t.Fatal("expected valid input")
	}
	if res.Exists() {
		t.Fatal("expected path to be missing")
	}
	if res.Type != Null {
		t.Fatalf("expected zero Null type for missing path, got %v", res.Type)
	}
	if res.Raw != "" {
		t.Fatalf("expected empty raw for missing path, got %q", res.Raw)
	}
}

// TestGetStrictCacheNotCrossInputs verifies that changing one byte does not
// reuse a stale validation outcome.
func TestGetStrictCacheNotCrossInputs(t *testing.T) {
	valid := `{"a":1}`
	invalid := `{"a":X}` // one byte changed: '1' -> 'X', now invalid

	// Validate the valid input first.
	r1 := GetStrict(valid, "a")
	if !r1.Valid || !r1.Exists() {
		t.Fatal("expected valid input with existing path")
	}

	// Query the one-byte-changed input. The cached valid outcome must not be
	// reused; the strict get must report the syntax error.
	r2 := GetStrict(invalid, "a")
	if r2.Valid {
		t.Fatal("expected invalid input after one-byte change; cache must not cross inputs")
	}
	if r2.Pos != 5 {
		t.Fatalf("expected first error position 5, got %d", r2.Pos)
	}
	if r2.Exists() {
		t.Fatal("expected no query result on invalid input")
	}

	// The valid input is still valid (cache correctness, not stale corruption).
	r3 := GetStrict(valid, "a")
	if !r3.Valid || !r3.Exists() {
		t.Fatal("expected valid input to remain valid")
	}
}

// TestGetStrictBytes exercises the byte-slice entry point.
func TestGetStrictBytes(t *testing.T) {
	res := GetStrictBytes([]byte(`{"a":1}garbage`), "a")
	if res.Valid || res.Pos != 7 {
		t.Fatalf("expected invalid at 7, got valid=%v pos=%d", res.Valid, res.Pos)
	}

	res = GetStrictBytes([]byte(`{"a":null}`), "a")
	if !res.Valid || !res.Exists() || res.Type != Null {
		t.Fatalf("expected existing null, got valid=%v exists=%v type=%v",
			res.Valid, res.Exists(), res.Type)
	}

	res = GetStrictBytes([]byte(`{"a":1}`), "b")
	if !res.Valid || res.Exists() {
		t.Fatalf("expected valid missing path, got valid=%v exists=%v",
			res.Valid, res.Exists())
	}
}

// TestGetStrictLooseGetUnchanged documents that the loose Get still returns a
// value from a corrupt-tail input, while GetStrict reports the error.
func TestGetStrictLooseGetUnchanged(t *testing.T) {
	loose := Get(`{"a":1}garbage`, "a")
	if !loose.Exists() || loose.Type != Number || loose.Num != 1 {
		t.Fatalf("expected loose Get to still return 1, got exists=%v type=%v num=%v",
			loose.Exists(), loose.Type, loose.Num)
	}

	strict := GetStrict(`{"a":1}garbage`, "a")
	if strict.Valid || strict.Exists() {
		t.Fatal("expected strict Get to reject the corrupt-tail input")
	}
}

// TestGetStrictMoreCases covers additional valid and invalid inputs.
func TestGetStrictMoreCases(t *testing.T) {
	valids := []string{
		`{"a":1}`,
		`{"a":null,"b":[1,2,3]}`,
		`[1,2,3]`,
		`"hello"`,
		`42`,
		`true`,
		`null`,
		`  {"a":1}  `,
	}
	for _, c := range valids {
		if r := GetStrict(c, "a"); !r.Valid {
			t.Fatalf("expected %q to be valid, got pos=%d", c, r.Pos)
		}
	}

	invalids := []struct {
		in  string
		pos int
	}{
		{`{"a":1}garbage`, 7},
		{`{"a":1`, 6},
		{`{"a":X}`, 5},
		{``, 0},
		{`   `, 3},
		{`{"a":1}}`, 7},
	}
	for _, c := range invalids {
		if r := GetStrict(c.in, "a"); r.Valid || r.Pos != c.pos {
			t.Fatalf("expected %q invalid at %d, got valid=%v pos=%d",
				c.in, c.pos, r.Valid, r.Pos)
		}
	}
}
