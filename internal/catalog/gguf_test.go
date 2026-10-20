package catalog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
)

func TestParseGGUFContext(t *testing.T) {
	data := ggufFixture(t, 262144)
	value, err := ParseGGUFContext(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if value != 262144 {
		t.Fatalf("context = %d", value)
	}
}

func TestParseGGUFContextFailures(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want error
	}{
		{name: "bad magic", data: []byte("NOPE"), want: nil},
		{name: "truncated", data: ggufFixture(t, 65536)[:20], want: nil},
		{name: "missing", data: ggufWithoutContext(t), want: ErrGGUFContextNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseGGUFContext(bytes.NewReader(test.data))
			if err == nil {
				t.Fatal("expected error")
			}
			if test.want != nil && !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func ggufFixture(t *testing.T, context uint64) []byte {
	t.Helper()
	var buffer bytes.Buffer
	buffer.WriteString("GGUF")
	writeBinary(t, &buffer, uint32(3))
	writeBinary(t, &buffer, uint64(0))
	writeBinary(t, &buffer, uint64(2))
	writeGGUFString(t, &buffer, "general.architecture")
	writeBinary(t, &buffer, ggufTypeString)
	writeGGUFString(t, &buffer, "mistral")
	writeGGUFString(t, &buffer, "mistral.context_length")
	writeBinary(t, &buffer, ggufTypeUint64)
	writeBinary(t, &buffer, context)
	return buffer.Bytes()
}

func ggufWithoutContext(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	buffer.WriteString("GGUF")
	writeBinary(t, &buffer, uint32(3))
	writeBinary(t, &buffer, uint64(0))
	writeBinary(t, &buffer, uint64(1))
	writeGGUFString(t, &buffer, "general.architecture")
	writeBinary(t, &buffer, ggufTypeString)
	writeGGUFString(t, &buffer, "mistral")
	return buffer.Bytes()
}

func writeGGUFString(t *testing.T, buffer *bytes.Buffer, value string) {
	t.Helper()
	writeBinary(t, buffer, uint64(len(value)))
	buffer.WriteString(value)
}

func writeBinary(t *testing.T, buffer *bytes.Buffer, value any) {
	t.Helper()
	if err := binary.Write(buffer, binary.LittleEndian, value); err != nil {
		t.Fatal(err)
	}
}
