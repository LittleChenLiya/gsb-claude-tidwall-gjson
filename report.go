// Copyright 2024 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.
//
// Optional query reports: hit results plus canonical, re-queryable paths
// that point back into the original JSON.

package gjson

import (
	"sort"
	"strconv"
	"strings"
)

// EntryKind describes how a ReportEntry was produced.
type EntryKind int

const (
	// EntryMissing means the path did not match anything in the document.
	// Missing is reported separately from an explicit JSON null.
	EntryMissing EntryKind = iota
	// EntryHit means the value was found in the original JSON and the
	// entry carries its exact byte range.
	EntryHit
	// EntryNull means an explicit JSON null was found in the original
	// JSON. Like EntryHit it carries the exact byte range of the null
	// literal.
	EntryNull
	// EntryDerived means the value was synthesized and has no position in
	// the original JSON. This includes modifier output (such as @keys),
	// calculated values (such as the '#' count), piped results and
	// multipath subselectors. Derived entries never fabricate a source
	// index: Start and End are -1 and Path is empty.
	EntryDerived
)

// String returns a string representation of the entry kind.
func (k EntryKind) String() string {
	switch k {
	default:
		return "Missing"
	case EntryHit:
		return "Hit"
	case EntryNull:
		return "Null"
	case EntryDerived:
		return "Derived"
	}
}

// ReportEntry is a single value reported by Query.
type ReportEntry struct {
	// Path is the canonical path to the value. Object keys are escaped
	// with the same backslash format used by Escape (a key "a.b" becomes
	// "a\.b", quotes and backslashes are backslash-escaped), and array
	// elements keep their actual numeric index, so Get(json, Path)
	// resolves back to the same original value. Path is empty when the
	// value has no source location (EntryDerived) or denotes the
	// document root. For EntryMissing, Path is the concrete path that
	// was probed and did not match.
	Path string
	// Result is the matched value.
	Result Result
	// Kind describes how the entry was produced.
	Kind EntryKind
	// Start is the byte offset of the raw value in the original JSON,
	// or -1 when the value has no source location.
	Start int
	// End is the byte offset just past the raw value in the original
	// JSON, or -1 when the value has no source location.
	End int
	// Duplicate is true when the path to the value passes through an
	// object key that also appears on an earlier sibling. Per GJSON
	// semantics Get resolves a repeated key to its first occurrence, so
	// for a Duplicate entry Get(json, Path) may return a different
	// (earlier) value than the one reported here.
	Duplicate bool
}

// QueryReport is the result of Query. It reports every matched value
// together with a canonical path that points back into the original JSON.
type QueryReport struct {
	// Query is the path that was evaluated.
	Query string
	// Entries are the reported values, ordered by their position in the
	// original JSON. Entries without a source position (EntryDerived)
	// are reported last, in evaluation order.
	Entries []ReportEntry
}

// Values returns the Result of every entry.
func (r QueryReport) Values() []Result {
	values := make([]Result, len(r.Entries))
	for i, e := range r.Entries {
		values[i] = e.Result
	}
	return values
}

// Query evaluates path against json like Get does, and returns a report
// of every matched value along with a canonical, re-queryable path and
// the exact byte range in the original JSON.
//
// Semantics:
//   - Array filters and multi-result queries report one entry per matched
//     element, each keeping its actual array index in Path.
//   - For an unfiltered "#.tail" projection, array elements that were
//     skipped because tail did not exist are reported as EntryMissing
//     entries (null-filling), keeping their actual array index. Elements
//     excluded by a "#(...)#" filter are not reported at all.
//   - An explicit JSON null is reported as EntryNull, distinct from
//     EntryMissing.
//   - Values synthesized by modifiers, calculations, pipes or multipath
//     subselectors are reported as EntryDerived with no source position.
//   - Duplicate object keys resolve to the first occurrence when the
//     entry Path is re-queried; entries located under a repeated key are
//     flagged with Duplicate.
//
// The Get function is unchanged; Query is an optional addition.
func Query(json, path string) QueryReport {
	rep := QueryReport{Query: path}
	res := Get(json, path)
	proj, hasProj := findProjection(path)
	if !res.Exists() {
		rep.Entries = append(rep.Entries, ReportEntry{
			Path:  path,
			Kind:  EntryMissing,
			Start: -1,
			End:   -1,
		})
		return rep
	}
	var items []reportItem
	if hasProj && res.IsArray() {
		// multi-result query: one entry per element, each element keeps
		// its actual position in the original JSON.
		arr := res.Array()
		for i := 0; i < len(arr); i++ {
			offset := -1
			if i < len(res.Indexes) {
				offset = res.Indexes[i]
			}
			items = append(items, makeReportItem(json, arr[i], offset))
		}
		if proj.tail != "" {
			items = fillMissing(json, proj, items)
		}
		sort.SliceStable(items, func(i, j int) bool {
			return items[i].key < items[j].key
		})
	} else {
		items = append(items, makeReportItem(json, res, res.Index))
	}
	rep.Entries = make([]ReportEntry, len(items))
	for i, item := range items {
		rep.Entries[i] = item.entry
	}
	return rep
}

// QueryBytes is like Query but for a byte slice.
func QueryBytes(json []byte, path string) QueryReport {
	return Query(string(json), path)
}

// reportItem is a ReportEntry with its sort key.
type reportItem struct {
	key   int
	entry ReportEntry
}

// noSourceKey sorts entries without a source position last.
const noSourceKey = int(^uint(0) >> 1)

// makeReportItem builds a ReportEntry for a single result. offset is the
// claimed position of res.Raw in the original JSON; it is only trusted
// when the bytes at that position match res.Raw exactly. Anything else
// is reported as derived so that no index is ever fabricated.
func makeReportItem(json string, res Result, offset int) reportItem {
	entry := ReportEntry{Result: res, Start: -1, End: -1}
	if !res.Exists() {
		entry.Kind = EntryMissing
		return reportItem{key: noSourceKey, entry: entry}
	}
	if offset >= 0 && len(res.Raw) > 0 && offset+len(res.Raw) <= len(json) &&
		json[offset:offset+len(res.Raw)] == res.Raw {
		entry.Start = offset
		entry.End = offset + len(res.Raw)
		if path, dup, ok := findPath(json, offset); ok {
			entry.Path = path
			entry.Duplicate = dup
		}
		if res.Type == Null {
			entry.Kind = EntryNull
		} else {
			entry.Kind = EntryHit
		}
		return reportItem{key: offset, entry: entry}
	}
	entry.Kind = EntryDerived
	return reportItem{key: noSourceKey, entry: entry}
}

// projection describes a '#'-projection component of a query path.
type projection struct {
	parent   string // path to the projected array, "" for the root
	comp     string // the projection component as written
	tail     string // path after the projection, "" when none
	filtered bool   // true for "#(...)#" queries
}

// findProjection locates the first multi-result projection component in
// a query path: either "#" followed by more path (an unfiltered
// projection) or "#(...)#" (a filtered query returning all matches).
func findProjection(path string) (projection, bool) {
	if len(path) == 0 {
		return projection{}, false
	}
	switch path[0] {
	case '@', '!', '[', '{':
		// modifiers and multipath subselectors produce derived values
		return projection{}, false
	}
	comps, ok := splitPathComponents(path)
	if !ok {
		return projection{}, false
	}
	for i, comp := range comps {
		if comp == "#" && i < len(comps)-1 {
			return projection{
				parent: strings.Join(comps[:i], "."),
				comp:   comp,
				tail:   strings.Join(comps[i+1:], "."),
			}, true
		}
		if strings.HasPrefix(comp, "#(") && strings.HasSuffix(comp, ")#") {
			return projection{
				parent:   strings.Join(comps[:i], "."),
				comp:     comp,
				tail:     strings.Join(comps[i+1:], "."),
				filtered: true,
			}, true
		}
	}
	return projection{}, false
}

// splitPathComponents splits a query path on unescaped '.' separators,
// skipping over escaped characters and parenthesized query groups. It
// returns ok=false when the path contains a top-level pipe, in which
// case projection detection is not attempted.
func splitPathComponents(path string) ([]string, bool) {
	var comps []string
	start := 0
	for i := 0; i < len(path); i++ {
		switch path[i] {
		case '\\':
			i++
		case '|':
			return nil, false
		case '(':
			depth := 0
			for ; i < len(path); i++ {
				switch path[i] {
				case '\\':
					i++
				case '(':
					depth++
				case ')':
					depth--
				}
				if depth == 0 {
					break
				}
			}
		case '.':
			comps = append(comps, path[start:i])
			start = i + 1
		}
	}
	return append(comps, path[start:]), true
}

// fillMissing implements the null-filling semantics of unfiltered
// "#.tail" projections: every element of the projected array that was
// skipped because tail did not exist is reported as an EntryMissing
// entry with its actual array index. For filtered "#(...)#" queries,
// only elements that match the filter but lack the tail are reported;
// elements excluded by the filter are not reported at all.
func fillMissing(json string, proj projection, items []reportItem) []reportItem {
	var arr Result
	if proj.parent == "" {
		arr = Parse(json)
	} else {
		arr = Get(json, proj.parent)
	}
	if !arr.IsArray() {
		return items
	}
	n := len(arr.Array())
	filter := strings.TrimSuffix(proj.comp, "#")
	for j := 0; j < n; j++ {
		elemPath := strconv.Itoa(j)
		if proj.parent != "" {
			elemPath = proj.parent + "." + elemPath
		}
		el := Get(json, elemPath)
		if proj.filtered {
			// evaluate the filter against the element by wrapping it
			// in an array; elements excluded by the filter are not
			// reported at all
			if !el.Exists() || len(el.Raw) == 0 ||
				!Get("["+el.Raw+"]", filter).Exists() {
				continue
			}
		}
		if Get(json, elemPath+"."+proj.tail).Exists() {
			continue // covered by a projection result
		}
		key := noSourceKey
		if el.Exists() &&
			el.Index >= 0 && el.Index+len(el.Raw) <= len(json) &&
			len(el.Raw) > 0 && json[el.Index:el.Index+len(el.Raw)] == el.Raw {
			key = el.Index
		}
		items = append(items, reportItem{
			key: key,
			entry: ReportEntry{
				Path:  elemPath + "." + proj.tail,
				Kind:  EntryMissing,
				Start: -1,
				End:   -1,
			},
		})
	}
	return items
}

// findPath locates the JSON value that starts at offset and returns the
// canonical path to it. Object keys are escaped with Escape and array
// elements are addressed by their actual numeric index, so the returned
// path can be queried back with Get. dup is true when the path passes
// through an object key that also appears on an earlier sibling.
func findPath(json string, offset int) (path string, dup bool, ok bool) {
	comps, dup, ok := walkValue(json, skipWS(json, 0), offset, nil)
	if !ok {
		return "", false, false
	}
	return strings.Join(comps, "."), dup, true
}

// walkValue descends into the value starting at i looking for offset,
// accumulating escaped path components in acc.
func walkValue(json string, i, offset int, acc []string) ([]string, bool, bool) {
	if i == offset {
		return acc, false, true
	}
	if i < 0 || i >= len(json) {
		return nil, false, false
	}
	switch json[i] {
	case '{':
		i++
		seen := make(map[string]bool)
		for {
			i = skipWS(json, i)
			if i >= len(json) || json[i] == '}' || json[i] != '"' {
				return nil, false, false
			}
			raw, key := tostr(json[i:])
			if len(raw) == 0 {
				return nil, false, false
			}
			i = skipWS(json, i+len(raw))
			if i >= len(json) || json[i] != ':' {
				return nil, false, false
			}
			i = skipWS(json, i+1)
			end := valueEnd(json, i)
			if end < 0 {
				return nil, false, false
			}
			if offset >= i && offset < end {
				dup := seen[key]
				comps, ddup, ok := walkValue(json, i, offset,
					append(acc, Escape(key)))
				return comps, dup || ddup, ok
			}
			seen[key] = true
			i = skipWS(json, end)
			if i < len(json) && json[i] == ',' {
				i++
				continue
			}
			return nil, false, false
		}
	case '[':
		i++
		for idx := 0; ; idx++ {
			i = skipWS(json, i)
			if i >= len(json) || json[i] == ']' {
				return nil, false, false
			}
			end := valueEnd(json, i)
			if end < 0 {
				return nil, false, false
			}
			if offset >= i && offset < end {
				return walkValue(json, i, offset,
					append(acc, strconv.Itoa(idx)))
			}
			i = skipWS(json, end)
			if i < len(json) && json[i] == ',' {
				i++
				continue
			}
			return nil, false, false
		}
	}
	return nil, false, false
}

// skipWS returns the position of the first non-whitespace character at
// or after i.
func skipWS(json string, i int) int {
	for i < len(json) {
		switch json[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

// valueEnd returns the offset just past the JSON value that starts at i,
// or -1 when the value cannot be scanned.
func valueEnd(json string, i int) int {
	if i < 0 || i >= len(json) {
		return -1
	}
	switch json[i] {
	case '{', '[':
		if raw := squash(json[i:]); len(raw) > 0 {
			return i + len(raw)
		}
	case '"':
		if raw, _ := tostr(json[i:]); len(raw) > 0 {
			return i + len(raw)
		}
	case 'n', 't', 'f':
		if raw := tolit(json[i:]); len(raw) > 0 {
			return i + len(raw)
		}
	default:
		if raw, _ := tonum(json[i:]); len(raw) > 0 {
			return i + len(raw)
		}
	}
	return -1
}
