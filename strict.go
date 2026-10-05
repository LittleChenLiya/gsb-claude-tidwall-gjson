// Copyright 2026 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package gjson

// This file adds an explicit strict entry point for Get. Unlike Get, the
// strict entry validates the entire JSON document first, so that trailing
// garbage (for example `{"a":1}garbage`) is reported as a syntax error
// instead of being silently ignored once an earlier path has matched.

import "fmt"

// SyntaxError describes a JSON syntax error found while strictly validating
// the complete input. Offset is the byte position of the first character that
// could not be parsed, or len(json) when the input ends unexpectedly.
//
// SyntaxError never contains and never suggests repaired text: it is purely a
// read-only report about the original input.
type SyntaxError struct {
	// Offset is the zero-based byte position of the first invalid character.
	Offset int
}

// Error implements the error interface.
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("invalid JSON at byte offset %d", e.Offset)
}

// fnv1a64 computes a 64-bit FNV-1a digest over the complete input bytes. The
// digest is used to bind cached validation state to an exact sequence of
// bytes: changing a single byte changes the digest, so a cached verdict can
// never be reused against modified input.
func fnv1a64(data []byte) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for _, b := range data {
		h ^= uint64(b)
		h *= prime64
	}
	return h
}

// Strict is a strict JSON getter that fully validates each input before
// querying it. The zero value is ready to use.
//
// Strict is safe for reuse across calls with the same input: the validation
// result may be cached and reused. The cached state is bound to a digest of
// the complete input bytes, so it never carries over when the input changes,
// even by a single byte.
//
// A Strict value is not safe for concurrent use by multiple goroutines.
type Strict struct {
	// digest is the FNV-1a 64-bit digest of the bytes that valid refers to.
	digest uint64
	// length is the number of bytes that digest covers.
	length int
	// valid is the cached full-document validation verdict.
	valid bool
	// cached records whether this entry holds a verdict at all.
	cached bool
}

// validate runs full-document validation over data and returns a SyntaxError
// carrying the position of the first invalid byte, or nil when the complete
// input is valid JSON.
func validateStrict(data []byte) error {
	i, ok := validpayload(data, 0)
	if !ok {
		if i > len(data) {
			i = len(data)
		}
		return &SyntaxError{Offset: i}
	}
	return nil
}

// check validates json, reusing the cached verdict only when the cached
// digest matches the digest of the complete input bytes.
func (s *Strict) check(json []byte) error {
	digest := fnv1a64(json)
	if s.cached && s.length == len(json) && s.digest == digest {
		// Same complete input: the cached verdict still belongs to these
		// exact bytes and can be reused.
		if s.valid {
			return nil
		}
		// The cached failure does not retain a position; recomputing is
		// cheap and keeps the report tied to the current input.
		return validateStrict(json)
	}
	if err := validateStrict(json); err != nil {
		s.digest = digest
		s.length = len(json)
		s.valid = false
		s.cached = true
		return err
	}
	s.digest = digest
	s.length = len(json)
	s.valid = true
	s.cached = true
	return nil
}

// Get is the strict form of Get. It validates the entire json document first
// and only then runs the path query against the original input. The
// validation and the query share the same original input (no copy and no
// repaired document is produced).
//
// Return values fall into three distinct cases:
//
//   - A non-nil *SyntaxError means the input itself was malformed; Result is
//     the zero value and must not be interpreted as a missing path.
//   - A nil error with Result.Exists() == false means the input was legal
//     JSON but did not contain the path.
//   - A nil error with Result.Exists() == true means the path exists; a JSON
//     null is reported as Type == Null with Raw == "null", which Exists()
//     recognizes.
func (s *Strict) Get(json, path string) (Result, error) {
	// Validate over the original bytes and query the original string so that
	// both steps share one input identity.
	if err := s.check(stringBytes(json)); err != nil {
		return Result{}, err
	}
	return Get(json, path), nil
}

// GetBytes is the strict form of GetBytes. As with Get the complete byte
// slice is validated before the query and the original bytes feed both
// steps. The validation cache is keyed by a digest of the full slice, so
// mutating even one byte between calls forces revalidation.
func (s *Strict) GetBytes(json []byte, path string) (Result, error) {
	if json == nil {
		// Mirror the lenient GetBytes behavior for the nil special case.
		return Result{}, validateStrict(json)
	}
	if err := s.check(json); err != nil {
		return Result{}, err
	}
	return getBytes(json, path), nil
}

// StrictGet validates the entire json document before querying path and
// returns a *SyntaxError (with the first invalid byte position) when json is
// malformed. It performs no caching across different inputs: each call with
// new input is independently validated, so verdicts never leak between
// inputs. Use a Strict value when repeatedly querying the same document and
// you want to reuse the validation result.
//
//	{"a":1}garbage // querying "a" first reports the broken input rather than
//	               // returning the apparently matched value 1
//	{"a":null}    // returns an existing Null result for path "a"
//	{"a":1}       // querying a missing path returns a non-existing result
//	               // without any error
func StrictGet(json, path string) (Result, error) {
	var s Strict
	return s.Get(json, path)
}

// StrictGetBytes is the []byte form of StrictGet.
func StrictGetBytes(json []byte, path string) (Result, error) {
	var s Strict
	return s.GetBytes(json, path)
}
