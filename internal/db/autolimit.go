package db

import (
	"regexp"
	"strings"
)

const autoLimitN = 1000

var (
	selectRe = regexp.MustCompile(`(?is)^select\b`)
	limitRe  = regexp.MustCompile(`(?is)\blimit\b`)
)

// ApplyAutoLimit appends "LIMIT 1000" to a bare SELECT that has no existing
// LIMIT clause, returning the (possibly rewritten) SQL and whether it was
// capped. This is a simple regex heuristic, not a full SQL parser, per spec
// §4.1.
//
// Documented behavior for multi-statement input (e.g.
// "SELECT * FROM a; SELECT * FROM b"): the query is left entirely untouched
// (capped == false). Rewriting only the first statement risks silently
// corrupting the rest of the script (e.g. if the split on ';' is wrong for
// a string literal containing a semicolon), so the safer choice is to skip
// the heuristic altogether for multi-statement input and rely on the
// per-query context.WithTimeout (spec §4.1) as the safety net instead.
func ApplyAutoLimit(sql string) (string, bool) {
	trimmed := strings.TrimSpace(sql)
	if trimmed == "" {
		return sql, false
	}

	body := strings.TrimSuffix(trimmed, ";")
	if strings.Contains(body, ";") {
		return sql, false
	}

	if !selectRe.MatchString(trimmed) {
		return sql, false
	}
	if limitRe.MatchString(trimmed) {
		return sql, false
	}

	return trimmed + " LIMIT 1000", true
}
