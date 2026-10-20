package app

import "time"

type EventSink interface {
	Emit(Event) error
}

type Event struct {
	SchemaVersion string         `json:"schema_version"`
	RecordType    string         `json:"record_type"`
	Sequence      int            `json:"sequence"`
	Timestamp     time.Time      `json:"timestamp"`
	Command       Command        `json:"command"`
	TransactionID *string        `json:"transaction_id"`
	Kind          string         `json:"kind"`
	Level         string         `json:"level"`
	StepID        *string        `json:"step_id,omitempty"`
	Message       string         `json:"message"`
	Progress      *EventProgress `json:"progress,omitempty"`
	Issue         *Issue         `json:"issue,omitempty"`
	Attributes    map[string]any `json:"attributes"`
}

type EventProgress struct {
	Unit      string `json:"unit"`
	Completed int64  `json:"completed"`
	Total     *int64 `json:"total,omitempty"`
}
