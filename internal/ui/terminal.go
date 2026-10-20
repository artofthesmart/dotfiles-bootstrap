package ui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
)

type Terminal interface {
	Prompt(context.Context, string) (string, error)
	Write(string)
}

type PlainTerminal struct {
	reader *bufio.Reader
	writer io.Writer
}

func NewPlainTerminal(reader io.Reader, writer io.Writer) *PlainTerminal {
	return &PlainTerminal{reader: bufio.NewReader(reader), writer: writer}
}

func (terminal *PlainTerminal) Prompt(ctx context.Context, message string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := fmt.Fprint(terminal.writer, message); err != nil {
		return "", err
	}
	value, err := terminal.reader.ReadString('\n')
	if err != nil && len(value) == 0 {
		return "", err
	}
	return strings.TrimSpace(value), nil
}

func (terminal *PlainTerminal) Write(value string) {
	_, _ = io.WriteString(terminal.writer, value)
}
