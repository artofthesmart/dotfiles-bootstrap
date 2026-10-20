package catalog

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
)

var ErrGGUFContextNotFound = errors.New("GGUF context_length metadata not found")

const (
	ggufTypeUint8   = uint32(0)
	ggufTypeInt8    = uint32(1)
	ggufTypeUint16  = uint32(2)
	ggufTypeInt16   = uint32(3)
	ggufTypeUint32  = uint32(4)
	ggufTypeInt32   = uint32(5)
	ggufTypeFloat32 = uint32(6)
	ggufTypeBool    = uint32(7)
	ggufTypeString  = uint32(8)
	ggufTypeArray   = uint32(9)
	ggufTypeUint64  = uint32(10)
	ggufTypeInt64   = uint32(11)
	ggufTypeFloat64 = uint32(12)
)

func ParseGGUFContext(reader io.Reader) (uint64, error) {
	var magic [4]byte
	if _, err := io.ReadFull(reader, magic[:]); err != nil {
		return 0, fmt.Errorf("read GGUF magic: %w", err)
	}
	if string(magic[:]) != "GGUF" {
		return 0, errors.New("invalid GGUF magic")
	}

	version, err := readUint32(reader)
	if err != nil {
		return 0, fmt.Errorf("read GGUF version: %w", err)
	}
	if version < 2 || version > 3 {
		return 0, fmt.Errorf("unsupported GGUF version %d", version)
	}
	if _, err := readUint64(reader); err != nil {
		return 0, fmt.Errorf("read tensor count: %w", err)
	}
	metadataCount, err := readUint64(reader)
	if err != nil {
		return 0, fmt.Errorf("read metadata count: %w", err)
	}
	if metadataCount > 1_000_000 {
		return 0, fmt.Errorf("unreasonable GGUF metadata count %d", metadataCount)
	}

	for i := uint64(0); i < metadataCount; i++ {
		key, err := readGGUFString(reader)
		if err != nil {
			return 0, fmt.Errorf("read metadata key %d: %w", i, err)
		}
		valueType, err := readUint32(reader)
		if err != nil {
			return 0, fmt.Errorf("read metadata type for %q: %w", key, err)
		}
		if strings.HasSuffix(key, ".context_length") {
			value, err := readContextValue(reader, valueType)
			if err != nil {
				return 0, fmt.Errorf("read %q: %w", key, err)
			}
			if value == 0 {
				return 0, fmt.Errorf("%q is zero", key)
			}
			return value, nil
		}
		if err := skipGGUFValue(reader, valueType, 0); err != nil {
			return 0, fmt.Errorf("skip metadata %q: %w", key, err)
		}
	}
	return 0, ErrGGUFContextNotFound
}

func readContextValue(reader io.Reader, valueType uint32) (uint64, error) {
	switch valueType {
	case ggufTypeUint32:
		value, err := readUint32(reader)
		return uint64(value), err
	case ggufTypeUint64:
		return readUint64(reader)
	case ggufTypeInt32:
		value, err := readUint32(reader)
		if err != nil {
			return 0, err
		}
		signed := int32(value)
		if signed <= 0 {
			return 0, fmt.Errorf("invalid signed context %d", signed)
		}
		return uint64(signed), nil
	case ggufTypeInt64:
		value, err := readUint64(reader)
		if err != nil {
			return 0, err
		}
		signed := int64(value)
		if signed <= 0 {
			return 0, fmt.Errorf("invalid signed context %d", signed)
		}
		return uint64(signed), nil
	default:
		return 0, fmt.Errorf("unsupported context value type %d", valueType)
	}
}

func skipGGUFValue(reader io.Reader, valueType uint32, depth int) error {
	if depth > 8 {
		return errors.New("GGUF array nesting exceeds limit")
	}
	switch valueType {
	case ggufTypeUint8, ggufTypeInt8, ggufTypeBool:
		return discardExactly(reader, 1)
	case ggufTypeUint16, ggufTypeInt16:
		return discardExactly(reader, 2)
	case ggufTypeUint32, ggufTypeInt32, ggufTypeFloat32:
		return discardExactly(reader, 4)
	case ggufTypeUint64, ggufTypeInt64, ggufTypeFloat64:
		return discardExactly(reader, 8)
	case ggufTypeString:
		_, err := readGGUFString(reader)
		return err
	case ggufTypeArray:
		elementType, err := readUint32(reader)
		if err != nil {
			return err
		}
		count, err := readUint64(reader)
		if err != nil {
			return err
		}
		if count > 1_000_000 {
			return fmt.Errorf("unreasonable GGUF array count %d", count)
		}
		for i := uint64(0); i < count; i++ {
			if err := skipGGUFValue(reader, elementType, depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported GGUF metadata type %d", valueType)
	}
}

func readGGUFString(reader io.Reader) (string, error) {
	length, err := readUint64(reader)
	if err != nil {
		return "", err
	}
	if length > 16*1024*1024 {
		return "", fmt.Errorf("GGUF string length %d exceeds limit", length)
	}
	value := make([]byte, int(length))
	if _, err := io.ReadFull(reader, value); err != nil {
		return "", err
	}
	return string(value), nil
}

func readUint32(reader io.Reader) (uint32, error) {
	var value uint32
	err := binary.Read(reader, binary.LittleEndian, &value)
	return value, err
}

func readUint64(reader io.Reader) (uint64, error) {
	var value uint64
	err := binary.Read(reader, binary.LittleEndian, &value)
	return value, err
}

func discardExactly(reader io.Reader, count int64) error {
	copied, err := io.CopyN(io.Discard, reader, count)
	if err != nil {
		return err
	}
	if copied != count {
		return io.ErrUnexpectedEOF
	}
	return nil
}
