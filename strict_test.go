package gjson

import (
	"fmt"
	"testing"
)

func TestStrictGetSyntaxError(t *testing.T) {
	// Trailing garbage: the prefix looks like a hit for "a", but strict
	// validation must examine the entire document and fail first.
	res, err := StrictGet(`{"a":1}garbage`, "a")
	if err == nil {
		t.Fatal("expected a syntax error, got nil")
	}
	se, ok := err.(*SyntaxError)
	if !ok {
		t.Fatalf("expected *SyntaxError, got %T: %v", err, err)
	}
	if se.Offset != 7 {
		t.Fatalf("expected first error at offset 7, got %d", se.Offset)
	}
	if res.Exists() {
		t.Fatalf("syntax errors must not return an existing result, got %+v", res)
	}
	// The original lenient Get is unchanged: it still finds the prefix hit.
	loose := Get(`{"a":1}garbage`, "a")
	if !loose.Exists() || loose.Int() != 1 {
		t.Fatalf("lenient Get behavior changed: %+v", loose)
	}
}

func TestStrictGetExistingNull(t *testing.T) {
	// Legal JSON with an explicit null: the path exists and is null.
	res, err := StrictGet(`{"a":null}`, "a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Exists() {
		t.Fatal("existing null must report as existing")
	}
	if res.Type != Null || res.Raw != "null" {
		t.Fatalf("expected Null result with raw null, got type=%v raw=%q",
			res.Type, res.Raw)
	}
}

func TestStrictGetMissingPath(t *testing.T) {
	// Legal JSON without the path: no error and no existing value.
	res, err := StrictGet(`{"a":1}`, "b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Exists() {
		t.Fatalf("missing path must not exist, got %+v", res)
	}
}

func TestStrictGetThreeReportsAreDistinct(t *testing.T) {
	type outcome struct {
		kind   string
		offset int
	}
	classify := func(json, path string) outcome {
		res, err := StrictGet(json, path)
		if err != nil {
			se := err.(*SyntaxError)
			return outcome{"syntax", se.Offset}
		}
		if res.Exists() {
			return outcome{"exists:" + res.Type.String(), -1}
		}
		return outcome{"missing", -1}
	}
	cases := []struct {
		json, path string
		want       outcome
	}{
		{`{"a":1}garbage`, "a", outcome{"syntax", 7}},
		{`{"a":null}`, "a", outcome{"exists:Null", -1}},
		{`{"a":1}`, "b", outcome{"missing", -1}},
	}
	for _, c := range cases {
		if got := classify(c.json, c.path); got != c.want {
			t.Fatalf("%q path %q = %+v, want %+v", c.json, c.path, got, c.want)
		}
	}
}

func TestStrictCacheReuseSameInput(t *testing.T) {
	var s Strict
	for i := 0; i < 3; i++ {
		res, err := s.Get(`{"a":1}`, "a")
		if err != nil || !res.Exists() || res.Int() != 1 {
			t.Fatalf("call %d failed: res=%+v err=%v", i, res, err)
		}
	}
	// A different path on the same validated bytes still works.
	res, err := s.Get(`{"a":1}`, "b")
	if err != nil || res.Exists() {
		t.Fatalf("different path on cached input failed: %+v %v", res, err)
	}
}

func TestStrictCacheOneByteChange(t *testing.T) {
	var s Strict
	// Prime the cache with valid input.
	if _, err := s.Get(`{"a":1}`, "a"); err != nil {
		t.Fatal(err)
	}
	// Change a single byte so the document becomes malformed. The cached
	// valid verdict is bound to the complete-byte digest and must not be
	// reused.
	if _, err := s.Get(`{"a":1}X`, "a"); err == nil {
		t.Fatal("one-byte append must invalidate the cached validation")
	}
	// Flip a byte inside the document.
	if _, err := s.Get(`{X"a":1}`, "a"); err == nil {
		t.Fatal("one-byte flip must invalidate the cached validation")
	}
	// The same length and a changed content byte must also be detected.
	if _, err := s.Get(`{"b":1}`, "b"); err != nil {
		t.Fatalf("different same-length valid input must revalidate: %v", err)
	}
	if _, err := s.Get(`{"b":x}`, "b"); err == nil {
		t.Fatal("same-length invalid input must not use the prior verdict")
	}
	// Back to the original valid input: validation passes again.
	res, err := s.Get(`{"a":1}`, "a")
	if err != nil || res.Int() != 1 {
		t.Fatalf("returning to original input failed: %+v %v", res, err)
	}
}

func TestStrictCacheDoesNotCrossInputs(t *testing.T) {
	var s Strict
	// A Strict instance used serially on unrelated inputs validates each one.
	if _, err := s.Get(`{"a":1}garbage`, "a"); err == nil {
		t.Fatal("expected first input to be invalid")
	}
	res, err := s.Get(`{"a":1}`, "a")
	if err != nil || res.Int() != 1 {
		t.Fatalf("valid second input must not inherit the failure: %+v %v",
			res, err)
	}
	if _, err := s.Get(`{"a":2}oops`, "a"); err == nil {
		t.Fatal("invalid third input must be validated independently")
	}
	// Package-level StrictGet is stateless and never carries cache between
	// inputs either.
	_, err = StrictGet(`{"a":1}`, "a")
	if err != nil {
		t.Fatalf("stateless StrictGet failed: %v", err)
	}
}

func TestStrictGetBytes(t *testing.T) {
	// []byte form of the three core scenarios.
	if _, err := StrictGetBytes([]byte(`{"a":1}garbage`), "a"); err == nil {
		t.Fatal("expected syntax error for bytes with trailing garbage")
	}
	res, err := StrictGetBytes([]byte(`{"a":null}`), "a")
	if err != nil || !res.Exists() || res.Type != Null {
		t.Fatalf("existing null over bytes failed: %+v %v", res, err)
	}
	res, err = StrictGetBytes([]byte(`{"a":1}`), "b")
	if err != nil || res.Exists() {
		t.Fatalf("missing path over bytes failed: %+v %v", res, err)
	}

	// Mutating the backing array by one byte invalidates the cached verdict.
	var s Strict
	buf := []byte(`{"a":1}`)
	if _, err := s.GetBytes(buf, "a"); err != nil {
		t.Fatal(err)
	}
	buf[len(buf)-1] = 'X' // document is now `{"a":1X`
	if _, err := s.GetBytes(buf, "a"); err == nil {
		t.Fatal("mutated byte slice must not reuse the old validation")
	}
}

func TestStrictGetEmptyAndOffsets(t *testing.T) {
	cases := []struct {
		json   string
		offset int
	}{
		{"", 0},
		{"   ", 3},
		{"garbage", 0},
		{`{"a":1}garbage`, 7},
		{`{"a":}`, 5},
		{`[1,2`, 4},
	}
	for _, c := range cases {
		_, err := StrictGet(c.json, "a")
		if err == nil {
			t.Fatalf("%q: expected syntax error", c.json)
		}
		se := err.(*SyntaxError)
		if se.Offset != c.offset {
			t.Fatalf("%q: offset = %d, want %d", c.json, se.Offset, c.offset)
		}
	}
}

// ExampleStrictGet shows the three input scenarios: malformed input with
// trailing garbage, a legal document containing an explicit null, and a
// legal document missing the queried path.
func ExampleStrictGet() {
	// 1) Broken input: validation of the whole document fails before the
	// apparent prefix hit for "a" can be returned.
	if _, err := StrictGet(`{"a":1}garbage`, "a"); err != nil {
		fmt.Println("syntax error:", err)
	}

	// 2) Legal input with an existing null: the value exists and is null.
	if res, err := StrictGet(`{"a":null}`, "a"); err == nil && res.Exists() {
		fmt.Println("exists, type:", res.Type)
	}

	// 3) Legal input without the path: no error, but the value is absent.
	if res, err := StrictGet(`{"a":1}`, "b"); err == nil && !res.Exists() {
		fmt.Println("path missing")
	}
	// Output:
	// syntax error: invalid JSON at byte offset 7
	// exists, type: Null
	// path missing
}
