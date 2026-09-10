package db

import "context"

// ScanAllKeys drives a KVStore's cursor-based ScanKeys to accumulate pages
// of keys, stopping once nextCursor == 0. It preserves page order and never
// re-adds a key already seen (a key could reappear across a rehash during
// a live scan). Used by the browser's "load next page" flow.
func ScanAllKeys(ctx context.Context, kv KVStore, dbIndex int, pattern string, pageSize int) ([]string, error) {
	seen := make(map[string]struct{})
	var all []string
	var cursor uint64

	for {
		keys, next, err := kv.ScanKeys(ctx, dbIndex, pattern, cursor, pageSize)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			if _, dup := seen[k]; dup {
				continue
			}
			seen[k] = struct{}{}
			all = append(all, k)
		}
		if next == 0 {
			break
		}
		cursor = next
	}

	return all, nil
}

// RenderKVValue renders a KVValue for display, dispatching on its Type and
// ignoring every other (zero-valued) field. Returns a placeholder string for
// an unrecognized/zero-valued Type rather than panicking.
func RenderKVValue(v *KVValue) string {
	if v == nil {
		return ""
	}
	switch v.Type {
	case "string":
		return v.String
	case "hash":
		return renderHash(v.Hash)
	case "list":
		return renderLines(v.List)
	case "set":
		return renderLines(v.Set)
	case "zset":
		return renderZSet(v.ZSet)
	default:
		return ""
	}
}

func renderHash(h map[string]string) string {
	out := ""
	for k, v := range h {
		if out != "" {
			out += "\n"
		}
		out += k + ": " + v
	}
	return out
}

func renderLines(items []string) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += "\n"
		}
		out += item
	}
	return out
}

func renderZSet(members []ZMember) string {
	out := ""
	for i, m := range members {
		if i > 0 {
			out += "\n"
		}
		out += m.Member
	}
	return out
}
