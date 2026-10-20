package platform

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
)

var ErrApplicationNotFound = errors.New("application not found")

type StandardApplicationLocator struct {
	FileSystem       FileSystem
	Environment      EnvironmentBuilder
	OS               string
	ExecutablePaths  map[string][]string
	ApplicationPaths map[string][]string
}

func NewStandardApplicationLocator(filesystem FileSystem) StandardApplicationLocator {
	if filesystem == nil {
		filesystem = OSFileSystem{}
	}
	return StandardApplicationLocator{
		FileSystem:       filesystem,
		Environment:      NewEnvironmentBuilder(),
		OS:               runtime.GOOS,
		ExecutablePaths:  standardExecutablePaths(),
		ApplicationPaths: standardApplicationPaths(),
	}
}

func (locator StandardApplicationLocator) FindExecutable(name string) (Application, error) {
	for _, directory := range locator.pathDirectories() {
		for _, candidate := range executableCandidates(name, locator.os()) {
			fullPath := filepath.Join(directory, candidate)
			if locator.isExecutable(fullPath) {
				return Application{Name: name, Executable: fullPath, Source: "path"}, nil
			}
		}
	}
	for _, candidate := range locator.ExecutablePaths[strings.ToLower(name)] {
		if expanded := locator.expand(candidate); locator.isExecutable(expanded) {
			return Application{Name: name, Executable: expanded, Source: "standard_location"}, nil
		}
	}
	return Application{}, ErrApplicationNotFound
}

func (locator StandardApplicationLocator) FindApplication(name string) (Application, error) {
	if application, err := locator.FindExecutable(name); err == nil {
		return application, nil
	}
	for _, candidate := range locator.ApplicationPaths[strings.ToLower(name)] {
		expanded := locator.expand(candidate)
		info, err := locator.FileSystem.Stat(expanded)
		if err == nil && (info.IsDir() || info.Mode().IsRegular()) {
			return Application{Name: name, Executable: expanded, Source: "standard_location"}, nil
		}
	}
	return Application{}, ErrApplicationNotFound
}

func (locator StandardApplicationLocator) pathDirectories() []string {
	for _, entry := range locator.Environment.Build(nil) {
		if strings.HasPrefix(strings.ToUpper(entry), "PATH=") {
			return filepath.SplitList(entry[len("PATH="):])
		}
	}
	return nil
}

func (locator StandardApplicationLocator) isExecutable(name string) bool {
	info, err := locator.FileSystem.Stat(name)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	if locator.os() == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}

func (locator StandardApplicationLocator) expand(name string) string {
	replacer := strings.NewReplacer(
		"${HOME}", environmentValue(locator.Environment, "HOME"),
		"${LOCALAPPDATA}", environmentValue(locator.Environment, "LOCALAPPDATA"),
		"${PROGRAMFILES}", environmentValue(locator.Environment, "ProgramFiles"),
		"${PROGRAMFILES_X86}", environmentValue(locator.Environment, "ProgramFiles(x86)"),
	)
	return filepath.Clean(replacer.Replace(name))
}

func (locator StandardApplicationLocator) os() string {
	if locator.OS != "" {
		return locator.OS
	}
	return runtime.GOOS
}

func executableCandidates(name, osName string) []string {
	if osName != "windows" || strings.HasSuffix(strings.ToLower(name), ".exe") {
		return []string{name}
	}
	return []string{name + ".exe", name + ".cmd", name + ".bat"}
}

func environmentValue(environment EnvironmentBuilder, key string) string {
	for _, entry := range environment.Build(nil) {
		if candidate, value, ok := strings.Cut(entry, "="); ok && strings.EqualFold(candidate, key) {
			return value
		}
	}
	return ""
}

func standardExecutablePaths() map[string][]string {
	return map[string][]string{
		"code": {
			"/usr/local/bin/code",
			"/usr/bin/code",
			"/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code",
			"${LOCALAPPDATA}/Programs/Microsoft VS Code/bin/code.cmd",
			"${PROGRAMFILES}/Microsoft VS Code/bin/code.cmd",
			"${PROGRAMFILES_X86}/Microsoft VS Code/bin/code.cmd",
			"/usr/share/code/bin/code",
			"/snap/bin/code",
			"/var/lib/flatpak/exports/bin/com.visualstudio.code",
			"${HOME}/.local/share/flatpak/exports/bin/com.visualstudio.code",
		},
		"ollama": {
			"/usr/local/bin/ollama",
			"/usr/bin/ollama",
			"/Applications/Ollama.app/Contents/Resources/ollama",
			"${LOCALAPPDATA}/Programs/Ollama/ollama.exe",
			"${HOME}/.local/bin/ollama",
		},
		"copilot": {
			"${HOME}/.local/bin/copilot",
			"/usr/local/bin/copilot",
			"/usr/bin/copilot",
			"/opt/homebrew/bin/copilot",
			"/home/linuxbrew/.linuxbrew/bin/copilot",
			"${LOCALAPPDATA}/Programs/LocalAI/copilot.exe",
			"${LOCALAPPDATA}/Microsoft/WinGet/Links/copilot.exe",
		},
	}
}

func standardApplicationPaths() map[string][]string {
	return map[string][]string{
		"code": {
			"/Applications/Visual Studio Code.app",
			"${LOCALAPPDATA}/Programs/Microsoft VS Code/Code.exe",
			"${PROGRAMFILES}/Microsoft VS Code/Code.exe",
			"${PROGRAMFILES_X86}/Microsoft VS Code/Code.exe",
		},
		"ollama": {
			"/Applications/Ollama.app",
			"${LOCALAPPDATA}/Programs/Ollama/ollama app.exe",
		},
	}
}
