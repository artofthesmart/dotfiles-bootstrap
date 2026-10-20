package platform

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

const (
	PathMarkerStart = "# >>> local-ai managed PATH >>>"
	PathMarkerEnd   = "# <<< local-ai managed PATH <<<"
)

type PathChange struct {
	ConfigPath string
	BackupPath string
	Before     []byte
	After      []byte
	Preview    string
	Changed    bool
}

type PathMarker struct {
	FileSystem FileSystem
}

func (editor PathMarker) Plan(configPath, binDir string, windows bool) (PathChange, error) {
	if editor.FileSystem == nil || configPath == "" || binDir == "" {
		return PathChange{}, errors.New("PATH marker filesystem, config path, and bin directory are required")
	}
	before, err := editor.FileSystem.ReadFile(configPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return PathChange{}, fmt.Errorf("read shell configuration: %w", err)
	}
	block := unixPathBlock(binDir)
	if windows {
		block = powershellPathBlock(binDir)
	}
	if bytes.Contains(before, []byte(PathMarkerStart)) || bytes.Contains(before, []byte(PathMarkerEnd)) {
		if bytes.Contains(before, block) {
			return PathChange{ConfigPath: configPath, Before: before, After: before, Preview: string(block), Changed: false}, nil
		}
		return PathChange{}, errors.New("existing local-ai PATH marker is incomplete or differs from the requested path")
	}
	after := append([]byte(nil), before...)
	if len(after) > 0 && after[len(after)-1] != '\n' {
		after = append(after, '\n')
	}
	if len(after) > 0 {
		after = append(after, '\n')
	}
	after = append(after, block...)
	return PathChange{
		ConfigPath: configPath,
		BackupPath: configPath + ".local-ai.backup",
		Before:     before,
		After:      after,
		Preview:    string(block),
		Changed:    true,
	}, nil
}

func (editor PathMarker) Apply(change PathChange, consent bool) error {
	if !change.Changed {
		return nil
	}
	if !consent {
		return errors.New("PATH integration requires preview and consent")
	}
	if err := editor.FileSystem.MkdirAll(filepath.Dir(change.ConfigPath), 0o700); err != nil {
		return fmt.Errorf("create shell configuration directory: %w", err)
	}
	if len(change.Before) > 0 {
		if _, err := editor.FileSystem.Stat(change.BackupPath); err == nil {
			return errors.New("refuse to overwrite an existing PATH backup")
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("inspect PATH backup: %w", err)
		}
		if err := editor.FileSystem.WriteFile(change.BackupPath, change.Before, 0o600); err != nil {
			return fmt.Errorf("write PATH backup: %w", err)
		}
		if err := editor.FileSystem.Sync(change.BackupPath); err != nil {
			return fmt.Errorf("sync PATH backup: %w", err)
		}
	}
	if atomic, ok := editor.FileSystem.(AtomicFileSystem); ok {
		if err := atomic.WriteFileAtomic(change.ConfigPath, change.After, 0o600); err != nil {
			return fmt.Errorf("write PATH marker: %w", err)
		}
		return nil
	}
	if err := editor.FileSystem.WriteFile(change.ConfigPath, change.After, 0o600); err != nil {
		return fmt.Errorf("write PATH marker: %w", err)
	}
	return editor.FileSystem.Sync(change.ConfigPath)
}

func (editor PathMarker) Remove(configPath string) error {
	current, err := editor.FileSystem.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	start := bytes.Index(current, []byte(PathMarkerStart))
	end := bytes.Index(current, []byte(PathMarkerEnd))
	if start < 0 && end < 0 {
		return nil
	}
	if start < 0 || end < start {
		return errors.New("refuse to edit an incomplete local-ai PATH marker")
	}
	end += len(PathMarkerEnd)
	for end < len(current) && current[end] == '\n' {
		end++
	}
	prefixEnd := start
	if prefixEnd >= 2 && current[prefixEnd-1] == '\n' && current[prefixEnd-2] == '\n' {
		prefixEnd--
	}
	after := append([]byte(nil), current[:prefixEnd]...)
	after = append(after, current[end:]...)
	after = bytes.TrimSuffix(after, []byte("\n"))
	if len(after) > 0 {
		after = append(after, '\n')
	}
	if atomic, ok := editor.FileSystem.(AtomicFileSystem); ok {
		return atomic.WriteFileAtomic(configPath, after, 0o600)
	}
	return editor.FileSystem.WriteFile(configPath, after, 0o600)
}

func unixPathBlock(binDir string) []byte {
	value := strings.ReplaceAll(binDir, `"`, `\"`)
	return []byte(PathMarkerStart + "\n" +
		`export PATH="` + value + `:$PATH"` + "\n" +
		PathMarkerEnd + "\n")
}

func powershellPathBlock(binDir string) []byte {
	value := strings.ReplaceAll(binDir, "'", "''")
	return []byte(PathMarkerStart + "\n" +
		`if (($env:Path -split [IO.Path]::PathSeparator) -notcontains '` + value + `') { $env:Path = '` + value + `' + [IO.Path]::PathSeparator + $env:Path }` + "\n" +
		PathMarkerEnd + "\n")
}
