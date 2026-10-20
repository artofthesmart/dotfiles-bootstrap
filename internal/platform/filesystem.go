package platform

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type OSFileSystem struct{}

func (OSFileSystem) Open(name string) (io.ReadCloser, error) {
	return os.Open(name)
}

func (OSFileSystem) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(name)
}

func (OSFileSystem) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (OSFileSystem) WriteFile(name string, data []byte, mode fs.FileMode) error {
	return os.WriteFile(name, data, mode)
}

func (OSFileSystem) MkdirAll(name string, mode fs.FileMode) error {
	return os.MkdirAll(name, mode)
}

func (OSFileSystem) Chmod(name string, mode fs.FileMode) error {
	return os.Chmod(name, mode)
}

func (OSFileSystem) Rename(oldName, newName string) error {
	return os.Rename(oldName, newName)
}

func (OSFileSystem) Remove(name string) error {
	return os.Remove(name)
}

func (OSFileSystem) Sync(name string) error {
	return syncPath(name)
}

func (filesystem OSFileSystem) WriteFileAtomic(name string, data []byte, mode fs.FileMode) error {
	directory := filepath.Dir(name)
	file, err := os.CreateTemp(directory, "."+filepath.Base(name)+".partial-*")
	if err != nil {
		return fmt.Errorf("create atomic file: %w", err)
	}
	temporary := file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("set atomic file mode: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write atomic file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync atomic file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close atomic file: %w", err)
	}
	if err := os.Rename(temporary, name); err != nil {
		return fmt.Errorf("replace atomic file: %w", err)
	}
	keep = true
	if err := filesystem.Sync(directory); err != nil {
		return fmt.Errorf("sync atomic directory: %w", err)
	}
	return nil
}
