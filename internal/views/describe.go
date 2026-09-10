package views

import (
	"fmt"
	"strings"

	"dbtui/internal/db"
)

// FormatTableDescription renders a TableDescription (spec §5.5's describe
// modal: column/type/nullable/default, PK/FK/index sections) as plain text.
func FormatTableDescription(desc *db.TableDescription) string {
	var b strings.Builder

	b.WriteString("Columns:\n")
	for _, c := range desc.Columns {
		nullable := "NOT NULL"
		if c.Nullable {
			nullable = "NULL"
		}
		fmt.Fprintf(&b, "  %s %s %s", c.Name, c.DataType, nullable)
		if c.Default != "" {
			fmt.Fprintf(&b, " DEFAULT %s", c.Default)
		}
		b.WriteByte('\n')
	}

	if len(desc.PrimaryKeys) > 0 {
		fmt.Fprintf(&b, "\nPrimary Key: %s\n", strings.Join(desc.PrimaryKeys, ", "))
	}

	if len(desc.ForeignKeys) > 0 {
		b.WriteString("\nForeign Keys:\n")
		for _, fk := range desc.ForeignKeys {
			fmt.Fprintf(&b, "  %s -> %s.%s.%s\n", fk.Column, fk.RefSchema, fk.RefTable, fk.RefColumn)
		}
	}

	if len(desc.Indexes) > 0 {
		b.WriteString("\nIndexes:\n")
		for _, idx := range desc.Indexes {
			unique := ""
			if idx.Unique {
				unique = " UNIQUE"
			}
			fmt.Fprintf(&b, "  %s%s (%s)\n", idx.Name, unique, strings.Join(idx.Columns, ", "))
		}
	}

	return b.String()
}

// FormatIndexInfo renders the MongoDB IndexInfo describe modal (spec §5.5:
// "reuses the describe-modal shell").
func FormatIndexInfo(indexes []db.IndexInfo) string {
	var b strings.Builder
	b.WriteString("Indexes:\n")
	for _, idx := range indexes {
		unique := ""
		if idx.Unique {
			unique = " UNIQUE"
		}
		fmt.Fprintf(&b, "  %s%s (%s)\n", idx.Name, unique, strings.Join(idx.Columns, ", "))
	}
	return b.String()
}
