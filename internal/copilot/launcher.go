package copilot

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

const (
	DefaultOllamaBaseURL = "http://127.0.0.1:11434/v1"
	LauncherName         = "copilot-local"
)

var localAliasPattern = regexp.MustCompile(`^local-ai-[a-z0-9][a-z0-9._-]{0,79}:[a-z0-9][a-z0-9._-]{0,39}$`)

type ProviderEnvironment struct {
	BaseURL   string
	Type      string
	APIKey    string
	WireAPI   string
	WireModel string
	Model     string
	Offline   string
}

func NewProviderEnvironment(alias, endpoint string) (ProviderEnvironment, error) {
	if !localAliasPattern.MatchString(alias) {
		return ProviderEnvironment{}, copilotError(app.IssueConfigurationInvalid, "configure Copilot local model", errors.New("alias must be an exact local-ai name and tag"))
	}
	baseURL, err := normalizeOllamaBaseURL(endpoint)
	if err != nil {
		return ProviderEnvironment{}, copilotError(app.IssueCopilotProvider, "configure Copilot local provider", err)
	}
	return ProviderEnvironment{
		BaseURL:   baseURL,
		Type:      "openai",
		APIKey:    "",
		WireAPI:   "responses",
		WireModel: alias,
		Model:     alias,
		Offline:   "true",
	}, nil
}

func (environment ProviderEnvironment) Variables() []string {
	return []string{
		"COPILOT_PROVIDER_BASE_URL=" + environment.BaseURL,
		"COPILOT_PROVIDER_TYPE=" + environment.Type,
		"COPILOT_PROVIDER_API_KEY=" + environment.APIKey,
		"COPILOT_PROVIDER_WIRE_API=" + environment.WireAPI,
		"COPILOT_PROVIDER_WIRE_MODEL=" + environment.WireModel,
		"COPILOT_MODEL=" + environment.Model,
		"COPILOT_OFFLINE=" + environment.Offline,
	}
}

func LauncherPath(paths platform.UserPaths, goos string) string {
	name := LauncherName
	if goos == "windows" {
		name += ".cmd"
	}
	return filepath.Join(paths.BinDir, name)
}

func OfficialExecutablePath(paths platform.UserPaths, goos string) string {
	name := "copilot"
	if goos == "windows" {
		name += ".exe"
	}
	return filepath.Join(paths.BinDir, name)
}

func LauncherBytes(goos, executable string, environment ProviderEnvironment) ([]byte, error) {
	if strings.TrimSpace(executable) == "" || strings.ContainsAny(executable, "\r\n\x00") {
		return nil, copilotError(app.IssueCopilotLauncher, "generate Copilot local launcher", errors.New("official executable path is invalid"))
	}
	if environment.Type != "openai" || environment.WireAPI != "responses" || environment.Offline != "true" ||
		environment.Model == "" || environment.Model != environment.WireModel {
		return nil, copilotError(app.IssueCopilotProvider, "generate Copilot local launcher", errors.New("provider environment does not enforce the offline Ollama contract"))
	}
	if goos == "windows" {
		return windowsLauncher(executable, environment), nil
	}
	return posixLauncher(executable, environment), nil
}

func posixLauncher(executable string, environment ProviderEnvironment) []byte {
	var builder strings.Builder
	builder.WriteString("#!/bin/sh\nset -eu\n")
	for _, variable := range environment.Variables() {
		key, value, _ := strings.Cut(variable, "=")
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(shellQuote(value))
		builder.WriteByte('\n')
		builder.WriteString("export ")
		builder.WriteString(key)
		builder.WriteByte('\n')
	}
	builder.WriteString("exec ")
	builder.WriteString(shellQuote(executable))
	builder.WriteString(" --disable-client-telemetry \"$@\"\n")
	return []byte(builder.String())
}

func windowsLauncher(executable string, environment ProviderEnvironment) []byte {
	var builder strings.Builder
	builder.WriteString("@echo off\r\nsetlocal DisableDelayedExpansion\r\n")
	for _, variable := range environment.Variables() {
		builder.WriteString("set \"")
		builder.WriteString(strings.ReplaceAll(variable, "%", "%%"))
		builder.WriteString("\"\r\n")
	}
	builder.WriteByte('"')
	builder.WriteString(escapeBatchPath(executable))
	builder.WriteString("\" --disable-client-telemetry %*\r\n")
	builder.WriteString("exit /b %ERRORLEVEL%\r\n")
	return []byte(builder.String())
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func escapeBatchPath(value string) string {
	value = strings.ReplaceAll(value, "%", "%%")
	value = strings.ReplaceAll(value, "^", "^^")
	return value
}

func normalizeOllamaBaseURL(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return DefaultOllamaBaseURL, nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("Ollama provider URL must be an unauthenticated loopback URL")
	}
	if parsed.Scheme != "http" {
		return "", errors.New("Ollama provider URL must use loopback HTTP")
	}
	host := parsed.Hostname()
	if !strings.EqualFold(host, "localhost") {
		address := net.ParseIP(host)
		if address == nil || !address.IsLoopback() {
			return "", errors.New("Ollama provider URL must use a loopback host")
		}
	}
	path := strings.TrimRight(parsed.Path, "/")
	if path != "" && path != "/v1" {
		return "", errors.New("Ollama provider URL must use the /v1 endpoint")
	}
	port := parsed.Port()
	if port == "" {
		port = "11434"
	}
	if net.ParseIP(host) != nil && strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%s/v1", host, port), nil
}

func writeAtomic(filesystem platform.FileSystem, name string, data []byte, mode fs.FileMode) error {
	if filesystem == nil {
		return errors.New("filesystem is not configured")
	}
	if atomic, ok := filesystem.(platform.AtomicFileSystem); ok {
		return atomic.WriteFileAtomic(name, data, mode)
	}
	stage := name + ".new"
	if err := filesystem.WriteFile(stage, data, mode); err != nil {
		return err
	}
	if err := filesystem.Sync(stage); err != nil {
		_ = filesystem.Remove(stage)
		return err
	}
	if err := filesystem.Rename(stage, name); err != nil {
		_ = filesystem.Remove(stage)
		return err
	}
	if err := filesystem.Sync(name); err != nil {
		return err
	}
	return filesystem.Sync(filepath.Dir(name))
}
