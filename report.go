// Copyright 2024 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package gjson

import (
	"strconv"
)

// Hit represents a single value found by a Report query.
//
// Each hit carries a Path that points back to the exact position of the
// value in the original JSON. The Path can be passed to Get to retrieve
// the same value, making it suitable for editor positioning.
type Hit struct {
	// Path is the escaped GJSON path that points back to this value.
	// It can be passed to Get to retrieve the same value.
	Path string
	// Index is the byte offset of the value's raw JSON in the original
	// JSON. It is -1 when the value has no original position, such as a
	// value synthesized by a modifier or a missing value.
	Index int
	// End is the byte offset immediately after the value's raw JSON.
	// It is -1 for derived or missing values.
	End int
	// Result is the value.
	Result Result
	// Missing is true when the path did not resolve to a value. This is
	// only reported for multi-result queries where an array element is
	// missing a requested field. A JSON null value is not missing; it is
	// reported as a hit with Type set to Null.
	Missing bool
	// Derived is true when the value was synthesized by a modifier and
	// has no position in the original JSON. The Index and End fields are
	// -1 for derived values.
	Derived bool
}

// Report executes a path and returns every hit with a path that points
// back to the original JSON. The Get function is unchanged.
//
// Unlike Get, which returns the first matching value, Report returns all
// matching values. Each hit includes the real array index and the
// original byte range of the value. Values synthesized by modifiers are
// marked as derived. Missing fields in multi-result queries are reported
// separately from JSON null values.
func Report(json, path string) []Hit {
	if len(path) == 0 {
		return nil
	}
	if path[0] == '@' || path[0] == '!' {
		return reportModifier(json, path)
	}
	var hits []Hit
	i := skipWS(json, 0)
	if i >= len(json) {
		return nil
	}
	switch json[i] {
	case '{':
		reportObject(json, i+1, path, nil, &hits, false)
	case '[':
		reportArray(json, i+1, path, nil, &hits, false)
	}
	return hits
}

// reportModifier executes a modifier and marks the result as derived,
// since the output of a modifier has no position in the original JSON.
func reportModifier(json, path string) []Hit {
	var npath, rjson string
	var ok bool
	if path[0] == '@' && !DisableModifiers {
		npath, rjson, ok = execModifier(json, path)
	} else if path[0] == '!' {
		npath, rjson, ok = execStatic(json, path)
	}
	if !ok {
		return nil
	}
	// Apply the remaining path to the modifier result.
	var res Result
	if len(npath) > 0 && (npath[0] == '|' || npath[0] == '.') {
		res = Get(rjson, npath[1:])
	} else {
		res = Parse(rjson)
	}
	return []Hit{{
		Path:    path,
		Index:   -1,
		End:     -1,
		Result:  res,
		Derived: true,
	}}
}

// reportWalk dispatches into an object or array at the given position.
func reportWalk(json string, path string, trail []byte, hits *[]Hit, derived bool) {
	i := skipWS(json, 0)
	if i >= len(json) {
		return
	}
	switch json[i] {
	case '{':
		reportObject(json, i+1, path, trail, hits, derived)
	case '[':
		reportArray(json, i+1, path, trail, hits, derived)
	default:
		if path == "" {
			recordHit(json, i, trail, false, derived, hits)
		}
	}
}

// reportObject walks an object at position i (just after the opening
// brace) and records hits for the given path.
func reportObject(json string, i int, path string, trail []byte, hits *[]Hit, derived bool) {
	if path == "" {
		recordHit(json, i-1, trail, false, derived, hits)
		return
	}
	rp := parseObjectPath(path)
	if rp.piped {
		// The pipe replaces the current path. Apply it to this object.
		reportValue(json, i-1, rp.pipe, trail, hits, derived)
		return
	}
	for i < len(json) {
		i = skipWS(json, i)
		if i >= len(json) || json[i] == '}' {
			return
		}
		if json[i] != '"' {
			i++
			continue
		}
		i++ // skip opening quote
		key, kesc, end, ok := parseObjectKey(json, i)
		if !ok {
			return
		}
		i = end
		// skip whitespace and colon
		for i < len(json) && (json[i] <= ' ' || json[i] == ':') {
			i++
		}
		var pmatch bool
		if rp.wild {
			if kesc {
				pmatch = matchLimit(unescape(key), rp.part)
			} else {
				pmatch = matchLimit(key, rp.part)
			}
		} else {
			if kesc {
				pmatch = rp.part == unescape(key)
			} else {
				pmatch = rp.part == key
			}
		}
		if !pmatch {
			i = skipValue(json, i)
			continue
		}
		keyTrail := appendKeyTrail(trail, key, kesc)
		if !rp.more {
			recordHit(json, i, keyTrail, false, derived, hits)
			if rp.wild {
				continue // wildcards match all keys
			}
			return // first match, matching Get behavior for literal keys
		}
		reportValue(json, i, rp.path, keyTrail, hits, derived)
		if rp.wild {
			continue // wildcards match all keys
		}
		return // first match
	}
}

// reportArray walks an array at position i (just after the opening
// bracket) and records hits for the given path.
func reportArray(json string, i int, path string, trail []byte, hits *[]Hit, derived bool) {
	if path == "" {
		recordHit(json, i-1, trail, false, derived, hits)
		return
	}
	rp := parseArrayPath(path)
	if rp.piped {
		// The pipe replaces the current path. Apply it to this array.
		reportValue(json, i-1, rp.pipe, trail, hits, derived)
		return
	}
	if rp.arrch && rp.query.on {
		reportArrayFilter(json, i, rp, trail, hits, derived)
		return
	}
	if rp.arrch && rp.part == "#" {
		if rp.alogok {
			reportArrayAllField(json, i, rp, trail, hits, derived)
			return
		}
		if rp.more {
			reportArrayAll(json, i, rp, trail, hits, derived)
			return
		}
		// A bare '#' returns the element count, which has no position in
		// the original JSON. Skip it rather than fabricating an index.
		return
	}
	// Index access.
	var partidx int = -1
	if rp.part != "*" && rp.part != "?" {
		if n, ok := parseUint(rp.part); ok {
			partidx = int(n)
		}
	}
	reportArrayIndex(json, i, rp, partidx, trail, hits, derived)
}

// reportArrayFilter handles the '#(query)' and '#(query)#' array filters.
func reportArrayFilter(json string, i int, rp arrayPathResult, trail []byte, hits *[]Hit, derived bool) {
	var idx int
	for i < len(json) {
		i = skipWS(json, i)
		if i >= len(json) || json[i] == ']' {
			return
		}
		elemStart := i
		end, qval, ok := parseAny(json, i, true)
		if !ok {
			return
		}
		if reportQueryMatches(rp, qval) {
			elemTrail := appendTrail(trail, strconv.Itoa(idx))
			if rp.more {
				reportSubPath(qval, rp.path, elemTrail, json, elemStart, hits, derived)
			} else {
				recordHit(json, elemStart, elemTrail, false, derived, hits)
			}
			if !rp.query.all {
				return // first match only
			}
		}
		idx++
		i = end
	}
}

// reportArrayAllField handles the '#.field' syntax, which extracts a field
// from every array element. Missing fields are reported separately.
func reportArrayAllField(json string, i int, rp arrayPathResult, trail []byte, hits *[]Hit, derived bool) {
	var idx int
	for i < len(json) {
		i = skipWS(json, i)
		if i >= len(json) || json[i] == ']' {
			return
		}
		elemStart := i
		end, qval, ok := parseAny(json, i, true)
		if !ok {
			return
		}
		elemTrail := appendTrail(trail, strconv.Itoa(idx))
		subHits := make([]Hit, 0)
		reportValue(qval.Raw, 0, rp.alogkey, elemTrail, &subHits, derived)
		if len(subHits) == 0 {
			// The element exists but the field is missing. Report the
			// missing hit with the full path including the field name.
			missingTrail := appendTrail(elemTrail, rp.alogkey)
			recordHit(json, elemStart, missingTrail, true, derived, hits)
		} else {
			for _, sh := range subHits {
				if !derived {
					sh.Index += qval.Index
					sh.End += qval.Index
				}
				*hits = append(*hits, sh)
			}
		}
		idx++
		i = end
	}
}

// reportArrayAll handles the '#.path' syntax, which recurses into every
// array element.
func reportArrayAll(json string, i int, rp arrayPathResult, trail []byte, hits *[]Hit, derived bool) {
	var idx int
	for i < len(json) {
		i = skipWS(json, i)
		if i >= len(json) || json[i] == ']' {
			return
		}
		elemStart := i
		end, qval, ok := parseAny(json, i, true)
		if !ok {
			return
		}
		elemTrail := appendTrail(trail, strconv.Itoa(idx))
		if rp.more {
			reportSubPath(qval, rp.path, elemTrail, json, elemStart, hits, derived)
		} else {
			recordHit(json, elemStart, elemTrail, false, derived, hits)
		}
		idx++
		i = end
	}
}

// reportArrayIndex handles literal and wildcard array index access.
func reportArrayIndex(json string, i int, rp arrayPathResult, partidx int, trail []byte, hits *[]Hit, derived bool) {
	var idx int
	for i < len(json) {
		i = skipWS(json, i)
		if i >= len(json) || json[i] == ']' {
			return
		}
		elemStart := i
		end, qval, ok := parseAny(json, i, true)
		if !ok {
			return
		}
		var match bool
		switch {
		case rp.part == "*":
			match = true
		case rp.part == "?":
			match = matchLimit(strconv.Itoa(idx), rp.part)
		case partidx >= 0:
			match = idx == partidx
		}
		if match {
			elemTrail := appendTrail(trail, strconv.Itoa(idx))
			if rp.more {
				reportSubPath(qval, rp.path, elemTrail, json, elemStart, hits, derived)
			} else {
				recordHit(json, elemStart, elemTrail, false, derived, hits)
			}
			if rp.part != "*" && rp.part != "?" {
				return // literal index, first match
			}
		}
		idx++
		i = end
	}
}

// reportSubPath resolves a sub-path from an array element and records the
// hits. If the sub-path does not resolve, a missing hit is recorded.
// The elemStart parameter is the position of the element in the original
// JSON, used to adjust the byte offsets of nested hits.
func reportSubPath(qval Result, subPath string, elemTrail []byte, json string, elemStart int, hits *[]Hit, derived bool) {
	subHits := make([]Hit, 0)
	reportValue(qval.Raw, 0, subPath, elemTrail, &subHits, derived)
	if len(subHits) == 0 {
		recordHit(json, elemStart, elemTrail, true, derived, hits)
		return
	}
	for _, sh := range subHits {
		if !derived {
			sh.Index += qval.Index
			sh.End += qval.Index
		}
		*hits = append(*hits, sh)
	}
}

// reportValue dispatches into a value at position i. If the value is a
// scalar and the path is empty, the scalar is recorded. If the path is
// non-empty and the value is a scalar, nothing is recorded.
func reportValue(json string, i int, path string, trail []byte, hits *[]Hit, derived bool) {
	i = skipWS(json, i)
	if i >= len(json) {
		return
	}
	switch json[i] {
	case '{':
		reportObject(json, i+1, path, trail, hits, derived)
	case '[':
		reportArray(json, i+1, path, trail, hits, derived)
	default:
		if path == "" {
			recordHit(json, i, trail, false, derived, hits)
		}
	}
}

// reportQueryMatches evaluates a query against an array element.
func reportQueryMatches(rp arrayPathResult, qval Result) bool {
	var res Result
	if qval.Type == JSON {
		res = qval.Get(rp.query.path)
	} else {
		if rp.query.path != "" {
			return false
		}
		res = qval
	}
	return queryMatches(&rp, res)
}

// recordHit records a single hit. If missing is true, a missing hit is
// recorded. If derived is true, the Index and End are set to -1.
func recordHit(json string, i int, trail []byte, missing, derived bool, hits *[]Hit) {
	if missing {
		*hits = append(*hits, Hit{
			Path:    trailString(trail),
			Index:   -1,
			End:     -1,
			Missing: true,
		})
		return
	}
	i = skipWS(json, i)
	end, res, ok := parseAny(json, i, true)
	if !ok {
		*hits = append(*hits, Hit{
			Path:    trailString(trail),
			Index:   -1,
			End:     -1,
			Missing: true,
		})
		return
	}
	hit := Hit{
		Path:   trailString(trail),
		Index:  i,
		End:    end,
		Result: res,
	}
	if derived {
		hit.Index = -1
		hit.End = -1
		hit.Derived = true
	}
	*hits = append(*hits, hit)
}

// appendKeyTrail appends an escaped object key to the trail.
func appendKeyTrail(trail []byte, key string, kesc bool) []byte {
	if kesc {
		key = unescape(key)
	}
	return appendTrail(trail, key)
}

// appendTrail appends an escaped path component to the trail.
func appendTrail(trail []byte, comp string) []byte {
	trail = append(trail, '.')
	trail = append(trail, Escape(comp)...)
	return trail
}

// trailString converts the trail to a path string, stripping the leading
// dot that is added for every component.
func trailString(trail []byte) string {
	if len(trail) > 0 && trail[0] == '.' {
		return string(trail[1:])
	}
	return string(trail)
}

// skipWS skips whitespace characters.
func skipWS(json string, i int) int {
	for i < len(json) && json[i] <= ' ' {
		i++
	}
	return i
}

// skipValue skips a single JSON value and returns the position after it.
func skipValue(json string, i int) int {
	i = skipWS(json, i)
	if i >= len(json) {
		return i
	}
	switch json[i] {
	case '"':
		i++
		end, _, _, _ := parseString(json, i)
		return end
	case '{', '[':
		end, _ := parseSquash(json, i)
		return end
	case 't', 'f', 'n':
		end, _ := parseLiteral(json, i)
		return end
	default:
		end, _ := parseNumber(json, i)
		return end
	}
}

// parseObjectKey parses an object key at position i, just after the
// opening quote. It returns the raw key, whether it contains escapes,
// the position after the closing quote, and whether it was parsed
// successfully.
func parseObjectKey(json string, i int) (key string, kesc bool, end int, ok bool) {
	var s = i
	for ; i < len(json); i++ {
		if json[i] > '\\' {
			continue
		}
		if json[i] == '"' {
			return json[s:i], false, i + 1, true
		}
		if json[i] == '\\' {
			i++
			for ; i < len(json); i++ {
				if json[i] > '\\' {
					continue
				}
				if json[i] == '"' {
					// look for an escaped slash
					if json[i-1] == '\\' {
						n := 0
						for j := i - 2; j > 0; j-- {
							if json[j] != '\\' {
								break
							}
							n++
						}
						if n%2 == 0 {
							continue
						}
					}
					return json[s:i], true, i + 1, true
				}
			}
			break
		}
	}
	return json[s:], false, i, false
}
