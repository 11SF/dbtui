package main

// GetLogs returns the buffered log lines (in-memory ring buffer, oldest
// first, capped at its configured capacity).
func (a *App) GetLogs() []string {
	if a.logger == nil {
		return nil
	}
	return a.logger.Lines()
}
