package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
)

type jsonStream struct {
	mu       sync.Mutex
	writer   io.Writer
	sequence int
}

func newJSONStream(writer io.Writer) *jsonStream {
	return &jsonStream{writer: writer}
}

func (stream *jsonStream) Emit(event app.Event) error {
	if stream == nil || stream.writer == nil {
		return errors.New("JSON stream is unavailable")
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	event.SchemaVersion = app.EventSchemaVersion
	event.RecordType = "event"
	event.Sequence = stream.sequence
	if event.Attributes == nil {
		event.Attributes = map[string]any{}
	}
	if err := encodeLine(stream.writer, event); err != nil {
		return err
	}
	stream.sequence++
	return nil
}

func (stream *jsonStream) Result(result app.Result) error {
	if stream == nil || stream.writer == nil {
		return errors.New("JSON stream is unavailable")
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	result.SchemaVersion = app.EventSchemaVersion
	result.RecordType = "result"
	result.Sequence = stream.sequence
	normalizeResult(&result)
	if err := encodeLine(stream.writer, result); err != nil {
		return err
	}
	stream.sequence++
	return nil
}

func encodeLine(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode JSON stream record: %w", err)
	}
	return nil
}

func normalizeResult(result *app.Result) {
	if result.Issues == nil {
		result.Issues = []app.Issue{}
	}
	if result.Components == nil {
		result.Components = []app.Component{}
	}
	if result.ManualActions == nil {
		result.ManualActions = []app.ManualAction{}
	}
	for index := range result.Issues {
		if result.Issues[index].Remediations == nil {
			result.Issues[index].Remediations = []app.Remediation{}
		}
	}
	for index := range result.Components {
		if result.Components[index].Attributes == nil {
			result.Components[index].Attributes = map[string]any{}
		}
	}
}
