package media

type Status string

const (
	StatusPending  Status = "pending"
	StatusRunning  Status = "running"
	StatusFailed   Status = "failed"
	StatusSkipped  Status = "skipped"
	StatusComplete Status = "complete"
)
