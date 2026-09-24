package db

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// NullValue is the sentinel FormatValue returns for a SQL NULL, rendered as
// "NULL" by the result view.
type NullValue struct{}

func (NullValue) String() string { return "NULL" }

// MarshalJSON sends NullValue over the wire as JSON null rather than the
// default "{}" an empty struct would otherwise produce — the GUI's
// formatCell (and, for editing, its edit-vs-null handling) both key off the
// cell being JS `null`, not an object.
func (NullValue) MarshalJSON() ([]byte, error) { return json.Marshal(nil) }

// FormatValue converts a raw driver value into the representation stored in
// QueryResult.Rows for display.
func FormatValue(v any) any {
	if v == nil {
		return NullValue{}
	}

	switch val := v.(type) {
	case NullValue:
		return val
	case []byte:
		return string(val)
	case [16]byte:
		// pgx decodes postgres's uuid type into a raw [16]byte array (not
		// []byte) when scanned into `any`; without this case it falls
		// through to the %v default and prints as "[0 0 ... 1]".
		return formatUUID(val)
	case time.Time:
		return val.Format(time.RFC3339)
	case string:
		return val
	case bool:
		return strconv.FormatBool(val)
	case int:
		return strconv.Itoa(val)
	case int8:
		return strconv.FormatInt(int64(val), 10)
	case int16:
		return strconv.FormatInt(int64(val), 10)
	case int32:
		return strconv.FormatInt(int64(val), 10)
	case int64:
		return strconv.FormatInt(val, 10)
	case uint:
		return strconv.FormatUint(uint64(val), 10)
	case uint64:
		return strconv.FormatUint(val, 10)
	case float32:
		return strconv.FormatFloat(float64(val), 'f', -1, 32)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	default:
		return fmt.Sprintf("%v", val)
	}
}

// formatUUID renders 16 raw bytes in the canonical 8-4-4-4-12 hex form.
func formatUUID(b [16]byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
