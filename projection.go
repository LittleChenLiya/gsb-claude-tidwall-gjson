package gjson

// Explicit query projections.
//
// A Projection is an explicitly registered column plan that runs a single
// query over an array in the original JSON document and binds every column
// to the SAME matching array record in a single pass. It guarantees that:
//
//   - Multiple field paths registered on one plan are resolved against one
//     record at a time; columns are never filtered independently and then
//     stitched back together by position.
//   - A field that is absent from a record is reported as missing and is
//     distinct from an explicit JSON null. Both cases keep the byte index of
//     the owning record in the original document.
//   - The query is executed once along the truly matching records only.
//   - Columns produced by a modifier are marked as derived and carry the
//     name of the modifier that produced them. A derived value never borrows
//     a position from the original text; failed modifier fields keep the
//     index of the owning record instead of faking an original position.
//   - The original JSON document is only read, never rewritten.
//   - Output columns are emitted in the exact order of registration.

// ProjectionColumn describes one registered output column.
type ProjectionColumn struct {
	// Name is the column name used when a projection is rendered as JSON.
	// When empty it defaults to Field.
	Name string

	// Field is the path resolved relative to each matched array record,
	// for example "id" or "profile.email". An empty Field binds the whole
	// record.
	Field string

	// Modifier is the optional name of a registered modifier (see
	// AddModifier). When set, the column value is derived by running the
	// modifier over the raw JSON of the bound field; such columns are
	// reported as Derived with Source set to Modifier.
	Modifier string

	// ModifierArg is passed verbatim as the modifier argument.
	ModifierArg string
}

// ProjectionCell is one evaluated column value within a ProjectionRow.
type ProjectionCell struct {
	// Value is the projected value.
	//
	// A field that is missing from the owning record yields a zero Value
	// (Type == Null, empty Raw) with Missing set to true. An explicit JSON
	// null yields Type == Null with Raw == "null" and Missing == false.
	Value Result

	// Missing reports that the field did not exist on the record.
	Missing bool

	// Derived reports that the value was produced by a modifier rather than
	// being read from the original document.
	Derived bool

	// Source names the modifier that produced a Derived value. It is empty
	// for values read directly from the document.
	Source string

	// Failed reports that the modifier did not run to completion (it was not
	// registered, panicked, or returned no value). The record Index is
	// retained on Value.
	Failed bool
}

// ProjectionRow is one matched array record projected through the plan.
type ProjectionRow struct {
	// RecordIndex is the index of the record within its source array, i.e.
	// the original ordinal is preserved even after filtering.
	RecordIndex int

	// RecordOffset is the byte offset of the record inside the original JSON
	// document.
	RecordOffset int

	// Cells holds the projected columns, in registration order.
	Cells []ProjectionCell
}

// Projection is an explicit column plan over one JSON array.
type Projection struct {
	// Path locates the source array in the document, such as "users".
	Path string

	// Where is an optional gjson query predicate evaluated against every
	// array record, such as "active==true". Records for which the predicate
	// is false are skipped; the source array itself is traversed once.
	Where string

	// Columns is the registration list; its order fixes the output order.
	Columns []ProjectionColumn
}

// Run executes the projection against json and returns the projected rows.
// The input document is never modified.
func (p Projection) Run(json string) ([]ProjectionRow, error) {
	root := Get(json, p.Path)
	if !root.Exists() {
		// Nothing to project.
		return nil, nil
	}

	// Bind every column to the same set of records. The query is evaluated
	// exactly once against the original document; each element Result points
	// at a real record (its Index is the absolute byte offset in json).
	var records []Result
	if p.Where == "" {
		records = root.Array()
	} else {
		matched := Get(json, p.Path+".#("+p.Where+")#")
		records = matched.Array()
	}
	if len(records) == 0 {
		return nil, nil
	}

	// Map absolute byte offsets of records back to their original ordinal in
	// the source array so filtering never renumbers rows.
	ordinal := make(map[int]int, len(records))
	for i, rec := range root.Array() {
		ordinal[rec.Index] = i
	}

	rows := make([]ProjectionRow, 0, len(records))
	for _, rec := range records {
		row := ProjectionRow{
			RecordIndex:  ordinal[rec.Index],
			RecordOffset: rec.Index,
			Cells:        make([]ProjectionCell, 0, len(p.Columns)),
		}
		// All columns of this row are resolved against this single record.
		// Fields are never queried independently of one another.
		for _, col := range p.Columns {
			row.Cells = append(row.Cells, p.evalCell(rec, col))
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// RunBytes is the []byte form of Run.
func (p Projection) RunBytes(json []byte) ([]ProjectionRow, error) {
	return p.Run(string(json))
}

// evalCell binds one registered column to one already matched record.
func (p Projection) evalCell(rec Result, col ProjectionColumn) ProjectionCell {
	var cell ProjectionCell

	// Resolve the bound field relative to the matched record. An empty field
	// path binds the whole record.
	field := rec
	if col.Field != "" {
		field = rec.Get(col.Field)
	}
	if !field.Exists() {
		// Missing field: keep the owning record position so the cell can
		// never be confused with a value read from somewhere else, and mark
		// it explicitly to distinguish it from a JSON null. A modifier can
		// only annotate the missing cell; it never fabricates a value or a
		// source position for it.
		cell.Value = Result{Type: Null, Index: rec.Index}
		cell.Missing = true
		if col.Modifier != "" {
			cell.Derived = true
			cell.Source = col.Modifier
		}
		return cell
	}
	cell.Value = field

	if col.Modifier == "" {
		return cell
	}

	// Derived column: run the modifier over the bound field raw JSON. The
	// modifier never receives, and therefore can never fabricate, a position
	// inside the original document.
	fn, ok := modifiers[col.Modifier]
	if !ok {
		cell.Value = Result{Type: Null, Index: rec.Index}
		cell.Missing = false
		cell.Derived = true
		cell.Source = col.Modifier
		cell.Failed = true
		return cell
	}
	raw, failed := runModifierSafe(fn, cell.Value.Raw, col.ModifierArg)
	cell.Derived = true
	cell.Source = col.Modifier
	cell.Failed = failed
	if failed || raw == "" {
		// A failed derivation retains the record index instead of pretending
		// the output was found in the original text.
		cell.Value = Result{Type: Null, Index: rec.Index}
		cell.Failed = true
		return cell
	}
	derived := Parse(raw)
	if !derived.Exists() {
		cell.Value = Result{Type: Null, Index: rec.Index}
		cell.Failed = true
		return cell
	}
	// Parsed derived JSON has no meaningful position in the source; clear it
	// rather than preserving a bogus offset.
	derived.Index = 0
	cell.Value = derived
	return cell
}

// runModifierSafe invokes fn while converting a panic into a failed
// derivation.
func runModifierSafe(fn func(json, arg string) string, json, arg string) (out string, failed bool) {
	defer func() {
		if r := recover(); r != nil {
			out = ""
			failed = true
		}
	}()
	return fn(json, arg), false
}

// ColumnNames returns the effective column names in registration order.
func (p Projection) ColumnNames() []string {
	names := make([]string, len(p.Columns))
	for i, col := range p.Columns {
		switch {
		case col.Name != "":
			names[i] = col.Name
		case col.Field != "":
			names[i] = col.Field
		default:
			names[i] = "_"
		}
	}
	return names
}

// JSON renders the projection as a JSON array of objects, one per matched
// record. Keys follow the registration order. Missing fields and failed
// derivations render as null. Null is indistinguishable from missing in the
// rendered JSON; the distinction is available on the cells themselves.
func (p Projection) JSON(rows []ProjectionRow) string {
	var b []byte
	b = append(b, '[')
	for r, row := range rows {
		if r > 0 {
			b = append(b, ',')
		}
		b = append(b, '{')
		for i, col := range p.Columns {
			if i > 0 {
				b = append(b, ',')
			}
			name := col.Name
			if name == "" {
				if col.Field != "" {
					name = col.Field
				} else {
					name = "_"
				}
			}
			b = AppendJSONString(b, name)
			b = append(b, ':')
			cell := row.Cells[i]
			raw := cell.Value.Raw
			if cell.Missing || cell.Failed || raw == "" {
				raw = "null"
			}
			b = append(b, raw...)
		}
		b = append(b, '}')
	}
	b = append(b, ']')
	return string(b)
}
