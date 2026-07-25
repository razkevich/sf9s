package api

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// Result is a query result flattened for tabular rendering. Column order
// mirrors the JSON field order Salesforce returns, which matches the order
// fields were written in the SELECT clause. Values keep their raw JSON text
// (numbers unmangled); relationship fields become dot paths and child
// subqueries are summarized as "(n rows)".
type Result struct {
	TotalSize      int
	Done           bool
	NextRecordsURL string
	Columns        []string
	Rows           [][]string
}

type queryResponse struct {
	TotalSize      int               `json:"totalSize"`
	Done           bool              `json:"done"`
	NextRecordsURL string            `json:"nextRecordsUrl"`
	Records        []json.RawMessage `json:"records"`
}

func buildResult(payload queryResponse) (*Result, error) {
	res := &Result{
		TotalSize:      payload.TotalSize,
		Done:           payload.Done,
		NextRecordsURL: payload.NextRecordsURL,
	}
	colIndex := map[string]int{}
	rowMaps := make([]map[string]string, 0, len(payload.Records))
	for _, raw := range payload.Records {
		cells := map[string]string{}
		err := flattenRecord(raw, "", 0, func(path, val string) {
			if _, ok := colIndex[path]; !ok {
				colIndex[path] = len(res.Columns)
				res.Columns = append(res.Columns, path)
			}
			cells[path] = val
		})
		if err != nil {
			return nil, fmt.Errorf("unexpected record shape: %w", err)
		}
		rowMaps = append(rowMaps, cells)
	}
	res.Columns = dropNullParentColumns(res.Columns, rowMaps)
	colIndex = map[string]int{}
	for i, col := range res.Columns {
		colIndex[col] = i
	}
	res.Rows = make([][]string, len(rowMaps))
	for i, cells := range rowMaps {
		row := make([]string, len(res.Columns))
		for path, val := range cells {
			if idx, ok := colIndex[path]; ok {
				row[idx] = val
			}
		}
		res.Rows[i] = row
	}
	return res, nil
}

// Merge combines a base result with a follow-up page whose column schema
// may differ (null relationships flatten differently per batch). Columns
// become the ordered union; every row is re-aligned to it.
func Merge(base, next *Result) *Result {
	merged := &Result{
		TotalSize:      base.TotalSize,
		Done:           next.Done,
		NextRecordsURL: next.NextRecordsURL,
	}
	colIndex := map[string]int{}
	for _, col := range append(append([]string{}, base.Columns...), next.Columns...) {
		if _, ok := colIndex[col]; !ok {
			colIndex[col] = len(merged.Columns)
			merged.Columns = append(merged.Columns, col)
		}
	}
	realign := func(cols []string, rows [][]string) {
		for _, row := range rows {
			out := make([]string, len(merged.Columns))
			for i, col := range cols {
				if i < len(row) {
					out[colIndex[col]] = row[i]
				}
			}
			merged.Rows = append(merged.Rows, out)
		}
	}
	realign(base.Columns, base.Rows)
	realign(next.Columns, next.Rows)
	return merged
}

// dropNullParentColumns removes a bare relationship column (produced by rows
// where the parent record is null) when dot-path child columns exist for it
// and the bare column holds no data of its own.
func dropNullParentColumns(columns []string, rowMaps []map[string]string) []string {
	hasChildren := func(col string) bool {
		prefix := col + "."
		for _, other := range columns {
			if len(other) > len(prefix) && other[:len(prefix)] == prefix {
				return true
			}
		}
		return false
	}
	kept := make([]string, 0, len(columns))
	for _, col := range columns {
		if hasChildren(col) {
			empty := true
			for _, cells := range rowMaps {
				if v, ok := cells[col]; ok && v != "" {
					empty = false
					break
				}
			}
			if empty {
				continue
			}
		}
		kept = append(kept, col)
	}
	return kept
}

// maxFlattenDepth bounds relationship nesting. Salesforce allows five levels
// up; the cap exists so a pathological response can't drive quadratic
// re-decoding of the record tree.
const maxFlattenDepth = 24

func flattenRecord(raw json.RawMessage, prefix string, depth int, add func(path, val string)) error {
	keys, vals, err := objectEntries(raw)
	if err != nil {
		return err
	}
	return flattenEntries(keys, vals, prefix, depth, add)
}

// flattenEntries walks already-decoded object entries, so a nested record is
// never decoded twice (once to test for a subquery, once to recurse).
func flattenEntries(keys []string, vals []json.RawMessage, prefix string, depth int, add func(path, val string)) error {
	if depth > maxFlattenDepth {
		return fmt.Errorf("record nesting deeper than %d levels", maxFlattenDepth)
	}
	for i, key := range keys {
		if key == "attributes" {
			continue
		}
		val := bytes.TrimSpace(vals[i])
		path := prefix + key
		switch {
		case len(val) == 0:
			add(path, "")
		case val[0] == '{':
			subKeys, subVals, err := objectEntries(val)
			if err != nil {
				return err
			}
			if n, ok := subqueryTotal(subKeys, subVals); ok {
				add(path, fmt.Sprintf("(%d rows)", n))
				continue
			}
			if err := flattenEntries(subKeys, subVals, path+".", depth+1, add); err != nil {
				return err
			}
		case val[0] == '"':
			var s string
			if err := json.Unmarshal(val, &s); err != nil {
				return err
			}
			add(path, s)
		case val[0] == 'n':
			add(path, "")
		default:
			add(path, string(val))
		}
	}
	return nil
}

func subqueryTotal(keys []string, vals []json.RawMessage) (int, bool) {
	hasRecords := false
	total := -1
	for i, k := range keys {
		switch k {
		case "records":
			hasRecords = true
		case "totalSize":
			var n int
			if err := json.Unmarshal(vals[i], &n); err == nil {
				total = n
			}
		}
	}
	if hasRecords && total >= 0 {
		return total, true
	}
	return 0, false
}

// objectEntries returns a JSON object's keys and raw values in document
// order, which encoding/json's map decoding would otherwise destroy.
func objectEntries(raw json.RawMessage) ([]string, []json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return nil, nil, err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, nil, fmt.Errorf("expected JSON object, got %v", tok)
	}
	var keys []string
	var vals []json.RawMessage
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, nil, err
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, nil, fmt.Errorf("expected object key, got %v", keyTok)
		}
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return nil, nil, err
		}
		keys = append(keys, key)
		vals = append(vals, val)
	}
	return keys, vals, nil
}
