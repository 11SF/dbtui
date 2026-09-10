package main

import (
	"fmt"

	"dbtui/internal/history"
)

// GetHistory returns past query/command entries, most recent first,
// optionally filtered by connection name and/or capped at limit (0 = all).
func (a *App) GetHistory(limit int, connNameFilter string) ([]history.HistoryEntry, error) {
	if a.hist == nil {
		return nil, fmt.Errorf("gui: history store not available")
	}
	return a.hist.List(history.ListOptions{ConnName: connNameFilter, Limit: limit})
}
