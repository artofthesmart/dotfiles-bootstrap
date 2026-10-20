package testkit

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrOutsideRoot = errors.New("path is outside the fake filesystem root")

type memoryNode struct {
	name    string
	mode    fs.FileMode
	data    []byte
	modTime time.Time
	synced  bool
}

func (n *memoryNode) clone() *memoryNode {
	copyNode := *n
	copyNode.data = append([]byte(nil), n.data...)
	return &copyNode
}

type MemoryFileSystem struct {
	mu       sync.RWMutex
	root     string
	nodes    map[string]*memoryNode
	failures *FailureInjector
	tick     int64
}

func NewMemoryFileSystem(root string, failures *FailureInjector) *MemoryFileSystem {
	cleaned := path.Clean("/" + strings.TrimPrefix(root, "/"))
	if cleaned == "/" {
		cleaned = "/sandbox"
	}
	return &MemoryFileSystem{
		root:     cleaned,
		failures: failures,
		nodes: map[string]*memoryNode{
			cleaned: {
				name:    path.Base(cleaned),
				mode:    fs.ModeDir | 0o700,
				modTime: time.Unix(0, 0).UTC(),
				synced:  true,
			},
		},
	}
}

func (m *MemoryFileSystem) Root() string {
	return m.root
}

func (m *MemoryFileSystem) Open(name string) (io.ReadCloser, error) {
	data, err := m.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func (m *MemoryFileSystem) Stat(name string) (fs.FileInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cleaned, err := m.resolve(name)
	if err != nil {
		return nil, pathError("stat", name, err)
	}
	node, ok := m.nodes[cleaned]
	if !ok {
		return nil, pathError("stat", name, fs.ErrNotExist)
	}
	return fileInfo{node: node.clone()}, nil
}

func (m *MemoryFileSystem) ReadFile(name string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cleaned, err := m.resolve(name)
	if err != nil {
		return nil, pathError("read", name, err)
	}
	node, ok := m.nodes[cleaned]
	if !ok {
		return nil, pathError("read", name, fs.ErrNotExist)
	}
	if node.mode.IsDir() {
		return nil, pathError("read", name, errors.New("is a directory"))
	}
	if node.mode.Perm()&0o444 == 0 {
		return nil, pathError("read", name, fs.ErrPermission)
	}
	return append([]byte(nil), node.data...), nil
}

func (m *MemoryFileSystem) WriteFile(name string, data []byte, mode fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cleaned, err := m.resolve(name)
	if err != nil {
		return pathError("write", name, err)
	}
	if err := m.fail("fs.write", BeforeMutation); err != nil {
		return pathError("write", name, err)
	}
	if err := m.requireWritableParent(cleaned); err != nil {
		return pathError("write", name, err)
	}
	if existing, ok := m.nodes[cleaned]; ok {
		if existing.mode.IsDir() {
			return pathError("write", name, errors.New("is a directory"))
		}
		if existing.mode.Perm()&0o222 == 0 {
			return pathError("write", name, fs.ErrPermission)
		}
	}
	m.nodes[cleaned] = &memoryNode{
		name:    path.Base(cleaned),
		mode:    mode.Perm(),
		data:    append([]byte(nil), data...),
		modTime: m.nextTime(),
		synced:  false,
	}
	m.markDirty(path.Dir(cleaned))
	if err := m.fail("fs.write", AfterMutation); err != nil {
		return pathError("write", name, err)
	}
	return nil
}

func (m *MemoryFileSystem) WriteFileAtomic(name string, data []byte, mode fs.FileMode) error {
	temporary := name + ".partial"
	if err := m.WriteFile(temporary, data, mode); err != nil {
		return err
	}
	if err := m.Sync(temporary); err != nil {
		return err
	}
	if err := m.Rename(temporary, name); err != nil {
		return err
	}
	if err := m.Sync(name); err != nil {
		return err
	}
	return m.Sync(path.Dir(name))
}

func (m *MemoryFileSystem) MkdirAll(name string, mode fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cleaned, err := m.resolve(name)
	if err != nil {
		return pathError("mkdir", name, err)
	}
	if cleaned == m.root {
		return nil
	}

	relative := strings.TrimPrefix(cleaned, m.root+"/")
	current := m.root
	for _, component := range strings.Split(relative, "/") {
		current = path.Join(current, component)
		if node, ok := m.nodes[current]; ok {
			if !node.mode.IsDir() {
				return pathError("mkdir", name, errors.New("path component is not a directory"))
			}
			continue
		}
		if err := m.fail("fs.mkdir", BeforeMutation); err != nil {
			return pathError("mkdir", current, err)
		}
		if err := m.requireWritableParent(current); err != nil {
			return pathError("mkdir", current, err)
		}
		m.nodes[current] = &memoryNode{
			name:    path.Base(current),
			mode:    fs.ModeDir | mode.Perm(),
			modTime: m.nextTime(),
			synced:  false,
		}
		m.markDirty(path.Dir(current))
		if err := m.fail("fs.mkdir", AfterMutation); err != nil {
			return pathError("mkdir", current, err)
		}
	}
	return nil
}

func (m *MemoryFileSystem) Rename(oldName, newName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	oldPath, err := m.resolve(oldName)
	if err != nil {
		return pathError("rename", oldName, err)
	}
	newPath, err := m.resolve(newName)
	if err != nil {
		return pathError("rename", newName, err)
	}
	if oldPath == m.root || newPath == m.root {
		return pathError("rename", oldName, fs.ErrPermission)
	}
	if err := m.fail("fs.rename", BeforeMutation); err != nil {
		return pathError("rename", oldName, err)
	}
	source, ok := m.nodes[oldPath]
	if !ok {
		return pathError("rename", oldName, fs.ErrNotExist)
	}
	if err := m.requireWritableParent(oldPath); err != nil {
		return pathError("rename", oldName, err)
	}
	if err := m.requireWritableParent(newPath); err != nil {
		return pathError("rename", newName, err)
	}
	if strings.HasPrefix(newPath+"/", oldPath+"/") {
		return pathError("rename", newName, errors.New("cannot rename a directory into itself"))
	}
	if destination, exists := m.nodes[newPath]; exists {
		if source.mode.IsDir() != destination.mode.IsDir() {
			return pathError("rename", newName, errors.New("source and destination types differ"))
		}
		if destination.mode.IsDir() && m.hasChildren(newPath) {
			return pathError("rename", newName, errors.New("directory not empty"))
		}
		delete(m.nodes, newPath)
	}

	moved := make(map[string]*memoryNode)
	for candidate, node := range m.nodes {
		if candidate == oldPath || strings.HasPrefix(candidate, oldPath+"/") {
			suffix := strings.TrimPrefix(candidate, oldPath)
			moved[newPath+suffix] = node
			delete(m.nodes, candidate)
		}
	}
	for candidate, node := range moved {
		node.name = path.Base(candidate)
		node.modTime = m.nextTime()
		node.synced = false
		m.nodes[candidate] = node
	}
	m.markDirty(path.Dir(oldPath))
	m.markDirty(path.Dir(newPath))
	if err := m.fail("fs.rename", AfterMutation); err != nil {
		return pathError("rename", oldName, err)
	}
	return nil
}

func (m *MemoryFileSystem) Remove(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cleaned, err := m.resolve(name)
	if err != nil {
		return pathError("remove", name, err)
	}
	if cleaned == m.root {
		return pathError("remove", name, fs.ErrPermission)
	}
	if err := m.fail("fs.remove", BeforeMutation); err != nil {
		return pathError("remove", name, err)
	}
	node, ok := m.nodes[cleaned]
	if !ok {
		return pathError("remove", name, fs.ErrNotExist)
	}
	if node.mode.IsDir() && m.hasChildren(cleaned) {
		return pathError("remove", name, errors.New("directory not empty"))
	}
	if err := m.requireWritableParent(cleaned); err != nil {
		return pathError("remove", name, err)
	}
	delete(m.nodes, cleaned)
	m.markDirty(path.Dir(cleaned))
	if err := m.fail("fs.remove", AfterMutation); err != nil {
		return pathError("remove", name, err)
	}
	return nil
}

func (m *MemoryFileSystem) Sync(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cleaned, err := m.resolve(name)
	if err != nil {
		return pathError("sync", name, err)
	}
	if err := m.fail("fs.sync", BeforeMutation); err != nil {
		return pathError("sync", name, err)
	}
	node, ok := m.nodes[cleaned]
	if !ok {
		return pathError("sync", name, fs.ErrNotExist)
	}
	node.synced = true
	if err := m.fail("fs.sync", AfterMutation); err != nil {
		return pathError("sync", name, err)
	}
	return nil
}

func (m *MemoryFileSystem) Chmod(name string, mode fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cleaned, err := m.resolve(name)
	if err != nil {
		return pathError("chmod", name, err)
	}
	if err := m.fail("fs.chmod", BeforeMutation); err != nil {
		return pathError("chmod", name, err)
	}
	node, ok := m.nodes[cleaned]
	if !ok {
		return pathError("chmod", name, fs.ErrNotExist)
	}
	node.mode = node.mode.Type() | mode.Perm()
	node.modTime = m.nextTime()
	node.synced = false
	if err := m.fail("fs.chmod", AfterMutation); err != nil {
		return pathError("chmod", name, err)
	}
	return nil
}

func (m *MemoryFileSystem) UnsyncedPaths() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var paths []string
	for name, node := range m.nodes {
		if !node.synced {
			paths = append(paths, name)
		}
	}
	sort.Strings(paths)
	return paths
}

func (m *MemoryFileSystem) Paths() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	paths := make([]string, 0, len(m.nodes))
	for name := range m.nodes {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	return paths
}

func (m *MemoryFileSystem) resolve(name string) (string, error) {
	if name == "" {
		return "", fs.ErrInvalid
	}
	var cleaned string
	if path.IsAbs(name) {
		cleaned = path.Clean(name)
	} else {
		cleaned = path.Clean(path.Join(m.root, name))
	}
	if cleaned != m.root && !strings.HasPrefix(cleaned, m.root+"/") {
		return "", ErrOutsideRoot
	}
	return cleaned, nil
}

func (m *MemoryFileSystem) requireWritableParent(name string) error {
	parent := path.Dir(name)
	node, ok := m.nodes[parent]
	if !ok {
		return fs.ErrNotExist
	}
	if !node.mode.IsDir() {
		return errors.New("parent is not a directory")
	}
	if node.mode.Perm()&0o300 != 0o300 {
		return fs.ErrPermission
	}
	return nil
}

func (m *MemoryFileSystem) hasChildren(name string) bool {
	prefix := name + "/"
	for candidate := range m.nodes {
		if strings.HasPrefix(candidate, prefix) {
			return true
		}
	}
	return false
}

func (m *MemoryFileSystem) markDirty(name string) {
	if node, ok := m.nodes[name]; ok {
		node.synced = false
		node.modTime = m.nextTime()
	}
}

func (m *MemoryFileSystem) nextTime() time.Time {
	m.tick++
	return time.Unix(0, m.tick).UTC()
}

func (m *MemoryFileSystem) fail(operation string, phase FailurePhase) error {
	if m.failures == nil {
		return nil
	}
	return m.failures.Fail(operation, phase)
}

func pathError(operation, name string, err error) error {
	return &fs.PathError{Op: operation, Path: name, Err: err}
}

type fileInfo struct {
	node *memoryNode
}

func (f fileInfo) Name() string       { return f.node.name }
func (f fileInfo) Size() int64        { return int64(len(f.node.data)) }
func (f fileInfo) Mode() fs.FileMode  { return f.node.mode }
func (f fileInfo) ModTime() time.Time { return f.node.modTime }
func (f fileInfo) IsDir() bool        { return f.node.mode.IsDir() }
func (f fileInfo) Sys() any           { return nil }
