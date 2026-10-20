package platform

import (
	"os"
	"runtime"
	"sort"
	"strings"
)

type EnvironmentBuilder struct {
	base map[string]string
}

func NewEnvironmentBuilder() EnvironmentBuilder {
	keys := []string{
		"HOME", "PATH", "LANG", "LC_ALL", "TMPDIR",
		"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME",
	}
	if runtime.GOOS == "windows" {
		keys = []string{
			"SystemRoot", "ComSpec", "PATHEXT", "PATH", "TEMP", "TMP",
			"USERPROFILE", "LOCALAPPDATA", "APPDATA", "ProgramFiles",
			"ProgramFiles(x86)", "ProgramData",
		}
	}
	base := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			base[key] = value
		}
	}
	return EnvironmentBuilder{base: base}
}

func NewEnvironmentBuilderFrom(values []string) EnvironmentBuilder {
	base := make(map[string]string, len(values))
	for _, value := range values {
		key, entry, ok := strings.Cut(value, "=")
		if ok && key != "" {
			base[key] = entry
		}
	}
	return EnvironmentBuilder{base: base}
}

func (builder EnvironmentBuilder) Build(overrides []string) []string {
	values := make(map[string]string, len(builder.base)+len(overrides))
	for key, value := range builder.base {
		values[key] = value
	}
	for _, override := range overrides {
		key, value, ok := strings.Cut(override, "=")
		if ok && key != "" {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}
