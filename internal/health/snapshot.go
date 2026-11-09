package health

// Snapshot is returned by GET /api/health.
type Snapshot struct {
	API          string `json:"api"`
	Medialog     string `json:"medialog"`
	WriteJournal string `json:"write_journal"`
}
