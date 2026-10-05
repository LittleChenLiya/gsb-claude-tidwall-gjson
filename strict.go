// Copyright 2024 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.
//
// https://codeberg.com/tidwall/gjson

// Package gjson provides searching for json strings.
package gjson

import (
	"strconv"
	"sync"
)

// StrictResult is the outcome of a strict JSON query performed by GetStrict.
//
// Unlike Get, GetStrict validates the entire input before querying, so a
// payload whose front looks valid but whose tail is corrupt is reported as a
// syntax error rather than a matching value.
//
// A StrictResult distinguishes three situations:
//
//   - Syntax error: Valid is false and Pos is the byte offset of the first
//     invalid byte in the input. Result is the zero value and no query is
//     performed.
//   - Valid input, missing path: Valid is true and Result.Exists is false.
//   - Valid input, path present: Valid is true and Result.Exists is true.
//     A present JSON null is reported as existing (Type == Null, Raw == "null").
type StrictResult struct {
	Result
	// Valid reports whether the entire input passed full JSON validation.
	Valid bool
	// Pos is the zero-based byte offset of the first syntax error in the
	// input. It is zero when Valid is true.
	Pos int
}

// SyntaxError is returned by StrictResult.Err when the input is not valid
// JSON. Pos is the zero-based byte offset of the first invalid byte.
type SyntaxError struct {
	Pos int
}

// Error implements the error interface.
func (e *SyntaxError) Error() string {
	return "invalid json at position " + strconv.Itoa(e.Pos)
}

// Err returns a *SyntaxError carrying the position of the first invalid byte
// when the input failed full validation, otherwise nil.
func (r StrictResult) Err() error {
	if r.Valid {
		return nil
	}
	return &SyntaxError{Pos: r.Pos}
}

// GetStrict validates the entire json input before querying path.
//
// It is the strict counterpart of Get: the whole input must be valid JSON
// before any query is attempted, so a payload such as {"a":1}garbage is
// reported as a syntax error at the first corrupt byte instead of returning
// the value of a. The validation and the query operate on the same input
// bytes; no repaired or rewritten text is produced.
//
// The returned StrictResult distinguishes a syntax error, a valid input with
// a missing path, and a valid input whose path is present (including a present
// JSON null).
func GetStrict(json, path string) StrictResult {
	return strictGet(stringBytes(json), path)
}

// GetStrictBytes validates the entire json input before querying path.
// If working with bytes, this method is preferred over
// GetStrict(string(data), path).
func GetStrictBytes(json []byte, path string) StrictResult {
	return strictGet(json, path)
}

func strictGet(data []byte, path string) StrictResult {
	valid, pos := checkValidation(data)
	if !valid {
		return StrictResult{Valid: false, Pos: pos}
	}
	return StrictResult{Result: GetBytes(data, path), Valid: true}
}

// validationCacheMax bounds the number of cached validation outcomes.
const validationCacheMax = 4096

// validationKey is a full-content digest of the input bytes.
type validationKey struct {
	hash uint64
	len  int
}

type validationEntry struct {
	valid bool
	pos   int
}

var (
	validationMu    sync.Mutex
	validationCache map[validationKey]validationEntry
)

// validationDigest computes a full-content FNV-1a 64-bit digest of data.
// A one-byte change to the input changes the digest, so a cached validation
// outcome can never be reused for a different input.
func validationDigest(data []byte) validationKey {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(data); i++ {
		h ^= uint64(data[i])
		h *= prime64
	}
	return validationKey{hash: h, len: len(data)}
}

// checkValidation returns whether the entire input is valid JSON and, when it
// is not, the byte offset of the first invalid byte. Results are cached by a
// full-content digest so repeated queries on the same input are cheap, while
// a changed input never reuses a stale outcome.
func checkValidation(data []byte) (bool, int) {
	key := validationDigest(data)
	validationMu.Lock()
	if validationCache == nil {
		validationCache = make(map[validationKey]validationEntry)
	}
	if e, ok := validationCache[key]; ok {
		validationMu.Unlock()
		return e.valid, e.pos
	}
	validationMu.Unlock()

	pos, valid := validpayload(data, 0)

	validationMu.Lock()
	if len(validationCache) >= validationCacheMax {
		// The cache is bounded; resetting avoids unbounded growth and never
		// affects correctness because entries are content-addressed.
		validationCache = make(map[validationKey]validationEntry)
	}
	validationCache[key] = validationEntry{valid: valid, pos: pos}
	validationMu.Unlock()
	return valid, pos
}
