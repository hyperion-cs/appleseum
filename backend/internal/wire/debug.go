package wire

type DebugEvent struct {
	Cursor            string `json:"cursor"`
	HasResponse       bool   `json:"hasResponse"`
	ResponseBytes     int64  `json:"responseBytes"`
	ResponseTruncated bool   `json:"responseTruncated"`
	Id                int64  `json:"id"`
	At                string `json:"at"`
	Source            string `json:"source"`
	Method            string `json:"method"`
	Target            string `json:"target"`
	Status            int    `json:"status"`
	DurationMs        int64  `json:"durationMs"`
	Detail            string `json:"detail"`
}

// DebugInfo describes the running server above its journal.
type DebugInfo struct {
	Executable    string   `json:"executable"`
	Arguments     []string `json:"arguments"`
	StartedAt     string   `json:"startedAt"`
	UptimeSeconds int64    `json:"uptimeSeconds"`
	Version       string   `json:"version"`
	Runtime       string   `json:"runtime"`
	Goroutines    int      `json:"goroutines"`
	HeapBytes     int64    `json:"heapBytes"`
	Capacity      int      `json:"capacity"`
}

type DebugPage = Page[DebugEvent]

type DebugSelectionResult struct {
	Ids []int64 `json:"ids"`
}

type DebugExportRequest struct {
	Ids []int64 `json:"ids"`
}
