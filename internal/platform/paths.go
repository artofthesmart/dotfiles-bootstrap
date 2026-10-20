package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

type StandardPathProvider struct {
	LookupEnv func(string) (string, bool)
	HomeDir   func() (string, error)
	OS        string
}

func NewStandardPathProvider() StandardPathProvider {
	return StandardPathProvider{
		LookupEnv: os.LookupEnv,
		HomeDir:   os.UserHomeDir,
		OS:        runtime.GOOS,
	}
}

func (provider StandardPathProvider) UserPaths() (UserPaths, error) {
	lookup := provider.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	homeDir := provider.HomeDir
	if homeDir == nil {
		homeDir = os.UserHomeDir
	}
	home, err := homeDir()
	if err != nil || home == "" {
		return UserPaths{}, errors.New("user home directory is unavailable")
	}
	osName := provider.OS
	if osName == "" {
		osName = runtime.GOOS
	}
	switch osName {
	case "windows":
		local := firstEnvironment(lookup, "LOCALAPPDATA")
		if local == "" {
			local = filepath.Join(home, "AppData", "Local")
		}
		roaming := firstEnvironment(lookup, "APPDATA")
		if roaming == "" {
			roaming = filepath.Join(home, "AppData", "Roaming")
		}
		temp := firstEnvironment(lookup, "TEMP", "TMP")
		if temp == "" {
			temp = filepath.Join(local, "Temp")
		}
		return UserPaths{
			Home:      home,
			ConfigDir: filepath.Join(roaming, "LocalAI"),
			DataDir:   filepath.Join(local, "LocalAI"),
			CacheDir:  filepath.Join(local, "LocalAI", "Cache"),
			BinDir:    filepath.Join(local, "Programs", "LocalAI"),
			TempDir:   temp,
		}, nil
	case "darwin":
		temp := firstEnvironment(lookup, "TMPDIR")
		if temp == "" {
			temp = filepath.Join(home, "Library", "Caches", "TemporaryItems")
		}
		return UserPaths{
			Home:      home,
			ConfigDir: filepath.Join(home, "Library", "Application Support", "LocalAI"),
			DataDir:   filepath.Join(home, "Library", "Application Support", "LocalAI"),
			CacheDir:  filepath.Join(home, "Library", "Caches", "LocalAI"),
			BinDir:    filepath.Join(home, ".local", "bin"),
			TempDir:   temp,
		}, nil
	default:
		config := environmentOr(lookup, "XDG_CONFIG_HOME", filepath.Join(home, ".config"))
		data := environmentOr(lookup, "XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
		cache := environmentOr(lookup, "XDG_CACHE_HOME", filepath.Join(home, ".cache"))
		temp := environmentOr(lookup, "TMPDIR", os.TempDir())
		return UserPaths{
			Home:      home,
			ConfigDir: filepath.Join(config, "local-ai"),
			DataDir:   filepath.Join(data, "local-ai"),
			CacheDir:  filepath.Join(cache, "local-ai"),
			BinDir:    filepath.Join(home, ".local", "bin"),
			TempDir:   temp,
		}, nil
	}
}

func firstEnvironment(lookup func(string) (string, bool), keys ...string) string {
	for _, key := range keys {
		if value, ok := lookup(key); ok && value != "" {
			return value
		}
	}
	return ""
}

func environmentOr(lookup func(string) (string, bool), key, fallback string) string {
	if value, ok := lookup(key); ok && value != "" {
		return value
	}
	return fallback
}
