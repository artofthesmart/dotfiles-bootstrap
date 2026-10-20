package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

type EventKind string
type EventLevel string
type EventMessage string

const (
	EventOperationStarted   EventKind = "operation_started"
	EventPhaseStarted       EventKind = "phase_started"
	EventPhaseCompleted     EventKind = "phase_completed"
	EventProgressChanged    EventKind = "progress"
	EventConsentRequired    EventKind = "consent_required"
	EventManualAction       EventKind = "manual_action"
	EventIssue              EventKind = "issue"
	EventAssetChanged       EventKind = "asset_changed"
	EventOperationCompleted EventKind = "operation_completed"

	EventDebug   EventLevel = "debug"
	EventInfo    EventLevel = "info"
	EventWarning EventLevel = "warning"
	EventError   EventLevel = "error"

	MessageOperationStarted   EventMessage = "The transaction started."
	MessagePhaseStarted       EventMessage = "A transaction phase started."
	MessagePhaseCompleted     EventMessage = "A transaction phase completed."
	MessageProgress           EventMessage = "Transaction progress changed."
	MessageConsentRequired    EventMessage = "The transaction requires consent before mutation."
	MessageManualAction       EventMessage = "The transaction requires a manual action."
	MessageIssue              EventMessage = "The transaction recorded a privacy-safe issue."
	MessageAssetChanged       EventMessage = "A verified managed asset changed."
	MessageOperationCompleted EventMessage = "The transaction completed."
)

type EventAttributes struct {
	ApplicationID *string `json:"application_id,omitempty"`
	AssetID       string  `json:"asset_id,omitempty"`
	AssetType     string  `json:"asset_type,omitempty"`
	Attempt       *int    `json:"attempt,omitempty"`
	Bytes         *int64  `json:"bytes,omitempty"`
	CommandFamily string  `json:"command_family,omitempty"`
	ContextTokens *int    `json:"context_tokens,omitempty"`
	DryRun        *bool   `json:"dry_run,omitempty"`
	ExtensionID   string  `json:"extension_id,omitempty"`
	ModelName     string  `json:"model_name,omitempty"`
	Operation     string  `json:"operation,omitempty"`
	Ownership     string  `json:"ownership,omitempty"`
	Phase         string  `json:"phase,omitempty"`
	ProfileName   string  `json:"profile_name,omitempty"`
	Risk          string  `json:"risk,omitempty"`
	ServiceID     string  `json:"service_id,omitempty"`
	Status        string  `json:"status,omitempty"`
	Structured    *bool   `json:"structured,omitempty"`
	ToolName      string  `json:"tool_name,omitempty"`
	Verified      *bool   `json:"verified,omitempty"`
	Version       string  `json:"version,omitempty"`
}

type Event struct {
	SchemaVersion string          `json:"schema_version"`
	RecordType    string          `json:"record_type"`
	Sequence      int             `json:"sequence"`
	Timestamp     time.Time       `json:"timestamp"`
	Command       Command         `json:"command"`
	TransactionID *string         `json:"transaction_id"`
	Kind          EventKind       `json:"kind"`
	Level         EventLevel      `json:"level"`
	StepID        *string         `json:"step_id,omitempty"`
	Message       EventMessage    `json:"message"`
	Progress      *EventProgress  `json:"progress,omitempty"`
	Issue         *Issue          `json:"issue,omitempty"`
	Attributes    EventAttributes `json:"attributes"`
}

type EventProgress struct {
	Unit      string `json:"unit"`
	Completed int64  `json:"completed"`
	Total     *int64 `json:"total,omitempty"`
}

type EventSink interface {
	Emit(Event) error
}

type JSONEventWriter struct {
	mu       sync.Mutex
	writer   io.Writer
	sequence int
}

func NewJSONEventWriter(writer io.Writer) *JSONEventWriter {
	return &JSONEventWriter{writer: writer}
}

func (w *JSONEventWriter) Emit(event Event) error {
	if w == nil || w.writer == nil {
		return errors.New("event writer is not configured")
	}
	if err := event.validate(); err != nil {
		return err
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	event.Sequence = w.sequence
	encoded, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode structured event: %w", err)
	}
	encoded = append(encoded, '\n')
	written, err := w.writer.Write(encoded)
	if err != nil {
		return fmt.Errorf("write structured event: %w", err)
	}
	if written != len(encoded) {
		return io.ErrShortWrite
	}
	w.sequence++
	return nil
}

func (e Event) validate() error {
	if e.SchemaVersion != EventSchemaVersion || e.RecordType != "event" {
		return fmt.Errorf("%w: invalid event discriminator", ErrInvalidState)
	}
	if err := validateUTC("event.timestamp", e.Timestamp); err != nil {
		return err
	}
	if !validCommand(e.Command) || !uuidPattern.MatchString(pointerValue(e.TransactionID)) {
		return fmt.Errorf("%w: invalid event transaction identity", ErrInvalidState)
	}
	switch e.Kind {
	case EventOperationStarted, EventPhaseStarted, EventPhaseCompleted, EventProgressChanged, EventConsentRequired, EventManualAction, EventIssue, EventAssetChanged, EventOperationCompleted:
	default:
		return fmt.Errorf("%w: invalid event kind", ErrInvalidState)
	}
	switch e.Level {
	case EventDebug, EventInfo, EventWarning, EventError:
	default:
		return fmt.Errorf("%w: invalid event level", ErrInvalidState)
	}
	if !validEventMessage(e.Message) {
		return fmt.Errorf("%w: event message is not privacy allowlisted", ErrInvalidState)
	}
	if e.StepID != nil && !stepIDPattern.MatchString(*e.StepID) {
		return fmt.Errorf("%w: invalid event step_id", ErrInvalidState)
	}
	encoded, err := json.Marshal(e.Attributes)
	if err != nil {
		return err
	}
	var attributes SafeAttributes
	if err := json.Unmarshal(encoded, &attributes); err != nil {
		return err
	}
	if err := validateAttributes(attributes); err != nil {
		return err
	}
	if e.Issue != nil {
		if err := e.Issue.validate(); err != nil {
			return err
		}
	}
	if e.Progress != nil {
		if e.Progress.Completed < 0 || (e.Progress.Total != nil && *e.Progress.Total < 0) {
			return fmt.Errorf("%w: invalid event progress", ErrInvalidState)
		}
	}
	return nil
}

func (e Engine) emit(transaction Transaction, stepID string, kind EventKind, message EventMessage, attributes EventAttributes) error {
	if e.Events == nil {
		return nil
	}
	var step *string
	if stepID != "" {
		step = &stepID
	}
	transactionID := transaction.TransactionID
	level := EventInfo
	if kind == EventIssue {
		level = EventError
	}
	return e.Events.Emit(Event{
		SchemaVersion: EventSchemaVersion,
		RecordType:    "event",
		Timestamp:     e.now(),
		Command:       transaction.Command,
		TransactionID: &transactionID,
		Kind:          kind,
		Level:         level,
		StepID:        step,
		Message:       message,
		Attributes:    attributes,
	})
}

func validEventMessage(message EventMessage) bool {
	switch message {
	case MessageOperationStarted, MessagePhaseStarted, MessagePhaseCompleted, MessageProgress, MessageConsentRequired, MessageManualAction, MessageIssue, MessageAssetChanged, MessageOperationCompleted:
		return true
	default:
		return false
	}
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
