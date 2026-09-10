package db

import (
	"fmt"
	"strconv"
	"time"
)

// NullValue is the sentinel FormatValue returns for a SQL NULL, rendered as
// "NULL" by the result view.
type NullValue struct{}

func (NullValue) String() string { return "NULL" }

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
