package testkit

import (
	"os"
	"path/filepath"
	"testing"
)

type ManagerPaths struct {
	Root         string
	Home         string
	Config       string
	Data         string
	Cache        string
	LocalAppData string
	Temp         string
	GoCache      string
	GoPath       string
	Bin          string
}

func MemoryManagerPaths(root string) ManagerPaths {
	root = filepath.Clean(root)
	return ManagerPaths{
		Root:         root,
		Home:         filepath.Join(root, "home"),
		Config:       filepath.Join(root, "config"),
		Data:         filepath.Join(root, "data"),
		Cache:        filepath.Join(root, "cache"),
		LocalAppData: filepath.Join(root, "localappdata"),
		Temp:         filepath.Join(root, "temp"),
		GoCache:      filepath.Join(root, "go-cache"),
		GoPath:       filepath.Join(root, "go-path"),
		Bin:          filepath.Join(root, "bin", executableName("local-ai")),
	}
}

func NewTempManagerPaths(t testing.TB) ManagerPaths {
	t.Helper()
	repository := findRepositoryRoot(t)
	base := filepath.Join(repository, "build", "test-sandboxes")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatalf("create sandbox base: %v", err)
	}
	root, err := os.MkdirTemp(base, "manager-")
	if err != nil {
		t.Fatalf("create manager sandbox: %v", err)
	}
	paths := MemoryManagerPaths(root)
	for _, directory := range []string{
		paths.Home,
		paths.Config,
		paths.Data,
		paths.Cache,
		paths.LocalAppData,
		paths.Temp,
		paths.GoCache,
		paths.GoPath,
		filepath.Dir(paths.Bin),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatalf("create sandbox directory %s: %v", directory, err)
		}
	}
	realHome, _ := os.UserHomeDir()
	if realHome != "" && paths.Home == filepath.Clean(realHome) {
		t.Fatalf("sandbox home %s must not be the real home", paths.Home)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Errorf("remove manager sandbox: %v", err)
		}
	})
	return paths
}

func (p ManagerPaths) Environment(pathValue string) []string {
	return []string{
		"HOME=" + p.Home,
		"USERPROFILE=" + p.Home,
		"XDG_CONFIG_HOME=" + p.Config,
		"XDG_DATA_HOME=" + p.Data,
		"XDG_CACHE_HOME=" + p.Cache,
		"LOCALAPPDATA=" + p.LocalAppData,
		"TMPDIR=" + p.Temp,
		"TMP=" + p.Temp,
		"TEMP=" + p.Temp,
		"GOCACHE=" + p.GoCache,
		"GOPATH=" + p.GoPath,
		"PATH=" + pathValue,
	}
}

func findRepositoryRoot(t testing.TB) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatal("could not locate repository root")
		}
		current = parent
	}
}

func executableName(name string) string {
	if os.PathSeparator == '\\' {
		return name + ".exe"
	}
	return name
}
