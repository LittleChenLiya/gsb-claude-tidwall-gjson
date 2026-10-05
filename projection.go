// Copyright 2024 Joshua J Baker. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package gjson

import "strings"

// Projection is an explicit, pre-registered query plan that binds multiple
// field paths to the same array record.
//
// A projection walks the source array once, evaluates the record filter once
// per record, and reads every registered field from each matched record. This
// avoids the common correctness problem of filtering each field independently
// and zipping the results by position, which silently misaligns records the
// moment a field is missing.
//
// Output columns are emitted in the order fields are registered.
type Projection struct {
	fields []projField
}

type projField struct {
	name string
	path string
}

// NewProjection returns a new, empty projection.
func NewProjection() *Projection {
	return &Projection{}
}

// Add registers a field (column) with the projection. Columns are output in
// registration order. name is the output column name and path is a gjson path
// evaluated against each matched record. Add returns the projection so calls
// can be chained.
func (p *Projection) Add(name, path string) *Projection {
	p.fields = append(p.fields, projField{name: name, path: path})
	return p
}

// ProjectionValue is a single projected field value.
type ProjectionValue struct {
	// Name is the registered output column name.
	Name string
	// Result is the field value. Exists()==false means the field is missing,
	// which is distinct from an explicit JSON null (Exists()==true, Type==Null).
	Result
	// Derived is true when a modifier (or other transformation) produced the
	// value. A derived value's Index is not a position in the source document;
	// it is reported as 0 so the source position is never forged.
	Derived bool
}

// ProjectionRecord is one matched source record with all of its fields.
type ProjectionRecord struct {
	// Index is the original index of the record in the source array. It is
	// preserved even when individual fields fail to extract.
	Index int
	// Record is the raw source record. Its Index points into the source
	// document.
	Record Result
	// Values holds the field values in registration order.
	Values []ProjectionValue
}

// Run executes the projection against json. arrayPath is the path to the
// source array, optionally including a record query such as
// "users.#(active==true)". Every record that matches the query is returned, in
// source order, with its original index preserved. The source document is
// never modified.
func (p *Projection) Run(json, arrayPath string) []ProjectionRecord {
	navPath, query, hasQuery := parseProjectionSource(arrayPath)

	// Navigate to the source array.
	var arr Result
	if navPath == "" {
		arr = Parse(json)
	} else {
		arr = Get(json, navPath)
	}
	if !arr.Exists() || !arr.IsArray() {
		return nil
	}

	// Parse the record query once. It is evaluated once per record, never per
	// field.
	var rp arrayPathResult
	if hasQuery {
		qpath, op, value, _, _, _, ok := parseQuery(query)
		if !ok {
			return nil
		}
		rp.query.on = true
		rp.query.path = qpath
		rp.query.op = op
		rp.query.value = value
	}

	out := make([]ProjectionRecord, 0)
	arr.ForEach(func(key, record Result) bool {
		if hasQuery {
			if !queryMatches(&rp, record.Get(rp.query.path)) {
				return true
			}
		}
		// key.Num is the 0-based index of the record in the source array.
		recIndex := int(key.Num)
		rec := ProjectionRecord{
			Index:  recIndex,
			Record: record,
			Values: make([]ProjectionValue, len(p.fields)),
		}
		for i, f := range p.fields {
			val := record.Get(f.path)
			pv := ProjectionValue{Name: f.name, Result: val}
			switch {
			case !val.Exists():
				// Failed field: keep the record index so the gap is traceable
				// to the record it belonged to.
				pv.Index = recIndex
			case isSourceValue(json, val):
				// Verbatim slice of the source; Index is a real source position.
			default:
				// A modifier produced new text. Do not forge a source
				// position; mark the value derived and clear the index.
				pv.Index = 0
				pv.Derived = true
			}
			rec.Values[i] = pv
		}
		out = append(out, rec)
		return true
	})
	return out
}

// parseProjectionSource splits an array path into the navigation path to the
// array and the optional record query. A query begins with "#(" and sits at
// the start of the path (root array) or after the dot that follows the array
// navigation path.
func parseProjectionSource(arrayPath string) (navPath, query string, hasQuery bool) {
	idx := strings.Index(arrayPath, "#(")
	if idx < 0 {
		return arrayPath, "", false
	}
	navPath = strings.TrimSuffix(arrayPath[:idx], ".")
	query = arrayPath[idx:]
	return navPath, query, true
}

// isSourceValue reports whether val.Raw is a verbatim slice of the source
// document at val.Index. A modifier-derived value is new text and will not
// match, so its Index must not be presented as a source position.
func isSourceValue(json string, val Result) bool {
	if val.Index < 0 || val.Index >= len(json) || len(val.Raw) == 0 {
		return false
	}
	if val.Index+len(val.Raw) > len(json) {
		return false
	}
	for i := 0; i < len(val.Raw); i++ {
		if json[val.Index+i] != val.Raw[i] {
			return false
		}
	}
	return true
}
