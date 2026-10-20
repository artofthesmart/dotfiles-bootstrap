package vscode

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
)

const (
	ProfileName                     = "Local AI"
	OfficialOllamaExtensionID       = "Ollama.ollama"
	BundledCopilotChatExtensionID   = "GitHub.copilot-chat"
	MinimumVSCodeVersion            = "1.127.0"
	MinimumOllamaExtensionVersion   = "0.0.13"
	OfficialRequirementEvidenceURL  = "https://github.com/ollama/ollama-vscode/blob/4b4d8c345a8929db4541e2a23f26fe07b0a5bca1/package.json"
	OfficialCopilotCapabilityURL    = "https://code.visualstudio.com/docs/agent-customization/language-models"
	OfficialRequirementEvidenceDate = "2026-10-20"
)

const (
	defaultCommandTimeout = 30 * time.Second
	defaultRetryDelay     = time.Second
	defaultRetryAttempts  = 10
	maxDiagnosticBytes    = 4096
)

var (
	ErrInvalidAlias          = errors.New("local AI alias must start with local-ai- and include an exact tag")
	ErrRepositoryUnavailable = errors.New("repository path is not an existing directory")
	ErrVSCodeUnavailable     = errors.New("Visual Studio Code CLI is unavailable")
	ErrVSCodeTooOld          = errors.New("Visual Studio Code does not meet the official Ollama extension requirement")

	secretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)[^\s"']+`),
		regexp.MustCompile(`(?i)((?:token|password|secret|api[_-]?key)\s*[=:]\s*)[^\s,;"']+`),
		regexp.MustCompile(`(?i)("(?:token|password|secret|api[_-]?key)"\s*:\s*")[^"]*(")`),
		regexp.MustCompile(`(?i)(https?://)[^/@\s]+:[^/@\s]+@`),
	}
	unixHomePattern    = regexp.MustCompile(`/home/[^/\s]+`)
	windowsHomePattern = regexp.MustCompile(`(?i)[A-Z]:\\Users\\[^\\\s]+`)
	localAliasPattern  = regexp.MustCompile(`^local-ai-[A-Za-z0-9][A-Za-z0-9._-]*:[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

type Clock interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type Options struct {
	Platform          platform.Capabilities
	Clock             Clock
	CommandTimeout    time.Duration
	ReadinessAttempts int
	ReadinessDelay    time.Duration
}

type Manager struct {
	platform          platform.Capabilities
	clock             Clock
	commandTimeout    time.Duration
	readinessAttempts int
	readinessDelay    time.Duration
}

type Version struct {
	Major int
	Minor int
	Patch int
	Raw   string
}

type Extension struct {
	ID      string
	Version string
}

type Diagnostic struct {
	Operation       string
	ExitCode        int
	Stdout          string
	Stderr          string
	OutputTruncated bool
}

type Status struct {
	Executable               string
	DiscoverySource          string
	Version                  string
	VersionSupported         bool
	RequiredVSCodeVersion    string
	ProfileName              string
	ProfileReady             bool
	OllamaExtensionInstalled bool
	OllamaExtensionVersion   string
	OllamaExtensionCurrent   bool
	CopilotChatBundled       bool
	CopilotChatAvailable     bool
	CopilotChatExtensionSeen bool
	CopilotChatSource        string
	RequirementEvidenceURL   string
	CopilotCapabilityURL     string
	RequirementEvidenceDate  string
	Diagnostics              []Diagnostic
	Issues                   []app.Issue
}

type Request struct {
	Alias      string
	Repository string
}

type Result struct {
	Status        Status
	Outcome       app.Outcome
	ExitCode      app.ExitCode
	Summary       string
	Issues        []app.Issue
	ManualActions []app.ManualAction
}

type CLIError struct {
	Operation string
	ExitCode  int
	Stdout    string
	Stderr    string
	Truncated bool
	Cause     error
}

func (e *CLIError) Error() string {
	if e == nil {
		return "VS Code CLI command failed"
	}
	if e.ExitCode != 0 {
		return fmt.Sprintf("%s failed with exit code %d", e.Operation, e.ExitCode)
	}
	return fmt.Sprintf("%s failed", e.Operation)
}

func (e *CLIError) Unwrap() error { return e.Cause }

func NewManager(options Options) *Manager {
	clock := options.Clock
	if clock == nil {
		clock = realClock{}
	}
	timeout := options.CommandTimeout
	if timeout <= 0 {
		timeout = defaultCommandTimeout
	}
	attempts := options.ReadinessAttempts
	if attempts <= 0 {
		attempts = defaultRetryAttempts
	}
	delay := options.ReadinessDelay
	if delay <= 0 {
		delay = defaultRetryDelay
	}
	return &Manager{
		platform:          options.Platform,
		clock:             clock,
		commandTimeout:    timeout,
		readinessAttempts: attempts,
		readinessDelay:    delay,
	}
}

func (m *Manager) Status(ctx context.Context) (Status, error) {
	status, err := m.inspect(ctx)
	status.Issues = healthIssues(status)
	return status, err
}

func (m *Manager) Doctor(ctx context.Context, request Request) Result {
	status, err := m.inspect(ctx)
	if err != nil {
		return m.failureResult(status, err)
	}
	issues := healthIssues(status)
	if len(issues) > 0 {
		return Result{
			Status:   status,
			Outcome:  app.OutcomeFailed,
			ExitCode: app.ExitForIssue(issues[0].Code),
			Summary:  "VS Code integration needs repair.",
			Issues:   issues,
		}
	}
	manual := manualActions(request.Alias)
	return Result{
		Status:        status,
		Outcome:       app.OutcomeManualRequired,
		ExitCode:      app.ExitManualRequired,
		Summary:       "Automated VS Code checks passed; protected UI actions remain.",
		Issues:        manualIssues(manual),
		ManualActions: manual,
	}
}

func (m *Manager) Configure(ctx context.Context, request Request) Result {
	if err := validateRequest(request); err != nil {
		return m.failureResult(Status{ProfileName: ProfileName}, err)
	}
	if err := m.validateRepository(request); err != nil {
		return m.failureResult(Status{ProfileName: ProfileName}, err)
	}

	status, err := m.inspect(ctx)
	if err != nil && !isProfileCLIError(err) {
		return m.failureResult(status, err)
	}
	if status.Executable == "" || !status.VersionSupported {
		if err == nil {
			err = ErrVSCodeTooOld
		}
		return m.failureResult(status, err)
	}

	openedForCreation := false
	if !status.ProfileReady {
		if _, launchErr := m.run(ctx, status.Executable, "create Local AI profile", profileOpenArgs("")); launchErr != nil {
			return m.failureResult(status, launchErr)
		}
		openedForCreation = true
		status, err = m.waitForProfile(ctx, status)
		if err != nil {
			return m.failureResult(status, err)
		}
	}

	if !status.OllamaExtensionInstalled || !status.OllamaExtensionCurrent {
		if _, err := m.run(ctx, status.Executable, "install official Ollama extension", []string{
			"--profile", ProfileName,
			"--install-extension", OfficialOllamaExtensionID,
			"--force",
		}); err != nil {
			return m.failureResult(status, err)
		}
		status, err = m.inspect(ctx)
		if err != nil {
			return m.failureResult(status, err)
		}
		if !status.OllamaExtensionInstalled || !status.OllamaExtensionCurrent {
			return m.failureResult(status, fmt.Errorf("%w: %s %s or newer was not verified", ErrVSCodeUnavailable, OfficialOllamaExtensionID, MinimumOllamaExtensionVersion))
		}
	}

	if request.Repository != "" || !openedForCreation {
		if _, err := m.run(ctx, status.Executable, "open Local AI profile", profileOpenArgs(request.Repository)); err != nil {
			return m.failureResult(status, err)
		}
	}

	manual := manualActions(request.Alias)
	return Result{
		Status:        status,
		Outcome:       app.OutcomeManualRequired,
		ExitCode:      app.ExitManualRequired,
		Summary:       "The Local AI profile and official Ollama extension are ready; protected UI actions remain.",
		Issues:        manualIssues(manual),
		ManualActions: manual,
	}
}

func (m *Manager) Open(ctx context.Context, request Request) Result {
	if err := validateRequest(request); err != nil {
		return m.failureResult(Status{ProfileName: ProfileName}, err)
	}
	if err := m.validateRepository(request); err != nil {
		return m.failureResult(Status{ProfileName: ProfileName}, err)
	}
	status, err := m.inspect(ctx)
	if err != nil {
		return m.failureResult(status, err)
	}
	if issues := healthIssues(status); len(issues) > 0 {
		return Result{Status: status, Outcome: app.OutcomeFailed, ExitCode: app.ExitForIssue(issues[0].Code), Summary: "VS Code integration is not ready.", Issues: issues}
	}
	if _, err := m.run(ctx, status.Executable, "open Local AI profile", profileOpenArgs(request.Repository)); err != nil {
		return m.failureResult(status, err)
	}
	manual := manualActions(request.Alias)
	return Result{
		Status:        status,
		Outcome:       app.OutcomeManualRequired,
		ExitCode:      app.ExitManualRequired,
		Summary:       "The Local AI profile is open; protected UI actions remain.",
		Issues:        manualIssues(manual),
		ManualActions: manual,
	}
}

func (m *Manager) Repair(ctx context.Context, request Request) Result {
	return m.Configure(ctx, request)
}

func (m *Manager) inspect(ctx context.Context) (Status, error) {
	status := Status{
		ProfileName:             ProfileName,
		RequiredVSCodeVersion:   MinimumVSCodeVersion,
		RequirementEvidenceURL:  OfficialRequirementEvidenceURL,
		CopilotCapabilityURL:    OfficialCopilotCapabilityURL,
		RequirementEvidenceDate: OfficialRequirementEvidenceDate,
	}
	if m.platform.Applications == nil || m.platform.Runner == nil {
		return status, ErrVSCodeUnavailable
	}
	application, err := m.platform.Applications.FindExecutable("code")
	if err != nil {
		application, err = m.platform.Applications.FindApplication("code")
	}
	if err != nil {
		return status, fmt.Errorf("%w: %v", ErrVSCodeUnavailable, err)
	}
	status.Executable = application.Executable
	status.DiscoverySource = application.Source

	versionResult, err := m.run(ctx, application.Executable, "read VS Code version", []string{"--version"})
	if err != nil {
		appendDiagnostic(&status, err)
		return status, err
	}
	version, err := ParseVersion(string(versionResult.Stdout))
	if err != nil {
		return status, fmt.Errorf("%w: invalid version output", ErrVSCodeUnavailable)
	}
	status.Version = version.Raw
	status.VersionSupported = version.AtLeast(mustVersion(MinimumVSCodeVersion))
	status.CopilotChatBundled = status.VersionSupported
	if status.CopilotChatBundled {
		status.CopilotChatAvailable = true
		status.CopilotChatSource = "bundled"
	}
	if !status.VersionSupported {
		return status, ErrVSCodeTooOld
	}

	extensions, listErr := m.listExtensions(ctx, application.Executable)
	if listErr != nil {
		appendDiagnostic(&status, listErr)
		return status, listErr
	}
	status.ProfileReady = true
	for _, extension := range extensions {
		if strings.EqualFold(extension.ID, OfficialOllamaExtensionID) {
			status.OllamaExtensionInstalled = true
			status.OllamaExtensionVersion = extension.Version
			installed, parseErr := ParseVersion(extension.Version)
			status.OllamaExtensionCurrent = parseErr == nil && installed.AtLeast(mustVersion(MinimumOllamaExtensionVersion))
		}
		if strings.EqualFold(extension.ID, BundledCopilotChatExtensionID) {
			status.CopilotChatExtensionSeen = true
			status.CopilotChatAvailable = true
			if !status.CopilotChatBundled {
				status.CopilotChatSource = "official_extension"
			}
		}
	}
	return status, nil
}

func (m *Manager) waitForProfile(ctx context.Context, prior Status) (Status, error) {
	status := prior
	var lastErr error
	for attempt := 0; attempt < m.readinessAttempts; attempt++ {
		extensions, err := m.listExtensions(ctx, status.Executable)
		if err == nil {
			status.ProfileReady = true
			status.Diagnostics = nil
			for _, extension := range extensions {
				if strings.EqualFold(extension.ID, OfficialOllamaExtensionID) {
					status.OllamaExtensionInstalled = true
					status.OllamaExtensionVersion = extension.Version
					parsed, parseErr := ParseVersion(extension.Version)
					status.OllamaExtensionCurrent = parseErr == nil && parsed.AtLeast(mustVersion(MinimumOllamaExtensionVersion))
				}
			}
			return status, nil
		}
		lastErr = err
		appendDiagnostic(&status, err)
		if attempt+1 < m.readinessAttempts {
			if err := m.clock.Sleep(ctx, m.readinessDelay); err != nil {
				return status, err
			}
		}
	}
	return status, fmt.Errorf("Local AI profile did not become ready after %d attempts: %w", m.readinessAttempts, lastErr)
}

func (m *Manager) listExtensions(ctx context.Context, executable string) ([]Extension, error) {
	result, err := m.run(ctx, executable, "list Local AI profile extensions", []string{
		"--profile", ProfileName,
		"--list-extensions",
		"--show-versions",
	})
	if err != nil {
		return nil, err
	}
	return ParseExtensions(string(result.Stdout)), nil
}

func (m *Manager) run(ctx context.Context, executable, operation string, args []string) (platform.CommandResult, error) {
	result, err := m.platform.Runner.Run(ctx, platform.Command{
		Name:        executable,
		Args:        append([]string(nil), args...),
		Timeout:     m.commandTimeout,
		OutputLimit: maxDiagnosticBytes,
	})
	if err == nil && result.ExitCode == 0 {
		return result, nil
	}
	return result, &CLIError{
		Operation: operation,
		ExitCode:  result.ExitCode,
		Stdout:    sanitizeDiagnostic(result.Stdout),
		Stderr:    sanitizeDiagnostic(result.Stderr),
		Truncated: result.StdoutTruncated || result.StderrTruncated,
		Cause:     err,
	}
}

func ParseVersion(output string) (Version, error) {
	line := strings.TrimSpace(output)
	if newline := strings.IndexAny(line, "\r\n"); newline >= 0 {
		line = line[:newline]
	}
	line = strings.TrimPrefix(line, "v")
	core := line
	if suffix := strings.IndexAny(core, "-+"); suffix >= 0 {
		core = core[:suffix]
	}
	parts := strings.Split(core, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return Version{}, fmt.Errorf("invalid semantic version %q", line)
	}
	values := [3]int{}
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return Version{}, fmt.Errorf("invalid semantic version %q", line)
		}
		values[index] = value
	}
	return Version{Major: values[0], Minor: values[1], Patch: values[2], Raw: line}, nil
}

func (v Version) AtLeast(required Version) bool {
	if v.Major != required.Major {
		return v.Major > required.Major
	}
	if v.Minor != required.Minor {
		return v.Minor > required.Minor
	}
	return v.Patch >= required.Patch
}

func ParseExtensions(output string) []Extension {
	var extensions []Extension
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id, version, found := strings.Cut(line, "@")
		if !found {
			version = "0.0.0"
		}
		extensions = append(extensions, Extension{ID: id, Version: version})
	}
	return extensions
}

func (m *Manager) failureResult(status Status, err error) Result {
	appendDiagnostic(&status, err)
	if errors.Is(err, context.Canceled) {
		return Result{Status: status, Outcome: app.OutcomeCancelled, ExitCode: app.ExitCancelled, Summary: "VS Code operation was cancelled.", Issues: []app.Issue{}}
	}
	code := app.IssueVSCodeIntegration
	summary := "Visual Studio Code integration failed."
	if errors.Is(err, ErrVSCodeTooOld) {
		code = app.IssuePrerequisiteVersion
		summary = fmt.Sprintf("VS Code %s or newer is required by official %s %s.", MinimumVSCodeVersion, OfficialOllamaExtensionID, MinimumOllamaExtensionVersion)
	} else if errors.Is(err, ErrInvalidAlias) || errors.Is(err, ErrRepositoryUnavailable) {
		code = app.IssueConfigurationInvalid
		summary = err.Error()
	} else if strings.Contains(strings.ToLower(err.Error()), "extension") {
		code = app.IssueVSCodeExtension
	}
	issue := app.Issue{
		Code:     code,
		Severity: app.SeverityError,
		Domain:   "vscode",
		Summary:  summary,
		Remediations: []app.Remediation{{
			Kind:       "command",
			Summary:    "Run doctor, review the redacted CLI diagnostics, then retry repair.",
			SafeForYes: true,
		}},
	}
	return Result{Status: status, Outcome: app.OutcomeFailed, ExitCode: app.ExitForIssue(code), Summary: summary, Issues: []app.Issue{issue}}
}

func healthIssues(status Status) []app.Issue {
	var issues []app.Issue
	add := func(code app.IssueCode, summary string) {
		issues = append(issues, app.Issue{
			Code: code, Severity: app.SeverityError, Domain: "vscode", Summary: summary,
			Remediations: []app.Remediation{{Kind: "command", Summary: "Run local-ai repair.", SafeForYes: true}},
		})
	}
	if status.Executable == "" {
		add(app.IssueVSCodeIntegration, "The VS Code CLI was not discovered on PATH or in a supported standard location.")
		return issues
	}
	if !status.VersionSupported {
		add(app.IssuePrerequisiteVersion, fmt.Sprintf("VS Code %s or newer is required.", MinimumVSCodeVersion))
	}
	if !status.ProfileReady {
		add(app.IssueVSCodeIntegration, "The Local AI profile is not ready.")
	}
	if !status.CopilotChatAvailable {
		add(app.IssueVSCodeIntegration, "The supported Copilot Chat capability is unavailable in the Local AI profile.")
	}
	if !status.OllamaExtensionInstalled {
		add(app.IssueVSCodeExtension, "The official Ollama.ollama extension is not installed in the Local AI profile.")
	} else if !status.OllamaExtensionCurrent {
		add(app.IssueVSCodeExtension, fmt.Sprintf("The Local AI profile needs %s %s or newer.", OfficialOllamaExtensionID, MinimumOllamaExtensionVersion))
	}
	return issues
}

func manualActions(alias string) []app.ManualAction {
	if alias == "" {
		alias = "local-ai-<model>:<tag>"
	}
	return []app.ManualAction{
		{ActionID: "vscode-workspace-trust", IssueCode: app.IssueVSCodeTrust, Surface: "vscode", Summary: "Open an existing repository normally and trust it only if appropriate; Restricted Mode hides local models and the manager never grants trust."},
		{ActionID: "vscode-reload-window", IssueCode: app.IssueManualApproval, Surface: "vscode", Summary: "Run Developer: Reload Window after extension or model changes when VS Code requests or requires a reload."},
		{ActionID: "vscode-select-local-model", IssueCode: app.IssueVSCodeModelSelection, Surface: "vscode_chat", Summary: fmt.Sprintf("In Chat choose Agent mode, then use the separate sparkle language-model picker and select the exact alias %s.", alias)},
		{ActionID: "vscode-refresh-diagnose", IssueCode: app.IssueVSCodeAliasUnavailable, Surface: "vscode_command_palette", Summary: "If the alias is absent, run Ollama: Refresh Models, then Ollama: Diagnose Models and inspect the Ollama output channel."},
		{ActionID: "vscode-configure-tools", IssueCode: app.IssueVSCodeToolLimit, Surface: "vscode_chat", Summary: "Use Configure Tools to keep at most 128 relevant tools; optionally evaluate github.copilot.chat.virtualTools.threshold, and never weaken tool approvals."},
		{ActionID: "vscode-utility-models", IssueCode: app.IssueVSCodeModelSelection, Surface: "vscode_settings", Summary: "Configure utility-task models separately only if desired; do not assume the selected Chat model controls every VS Code AI utility."},
		{ActionID: "vscode-context-recovery", IssueCode: app.IssueVSCodeAliasUnavailable, Surface: "vscode", Summary: fmt.Sprintf("If diagnostics show a 32K context, stop the session, verify %s is the 65K+ alias, restart the Ollama model, reload VS Code, and diagnose again.", alias)},
		{ActionID: "vscode-cloud-profile-safety", IssueCode: app.IssueManualApproval, Surface: "vscode", Summary: "Switch profiles and models explicitly when moving between local and cloud work; profiles are not billing boundaries, and every sensitive tool action still requires review and approval."},
	}
}

func manualIssues(actions []app.ManualAction) []app.Issue {
	seen := map[app.IssueCode]bool{}
	issues := make([]app.Issue, 0, len(actions))
	for _, action := range actions {
		if seen[action.IssueCode] {
			continue
		}
		seen[action.IssueCode] = true
		issues = append(issues, app.Issue{
			Code: action.IssueCode, Severity: app.SeverityWarning, Domain: "vscode", Summary: action.Summary,
			Remediations: []app.Remediation{{Kind: "manual", Summary: action.Summary, SafeForYes: false}},
		})
	}
	return issues
}

func profileOpenArgs(repository string) []string {
	args := []string{"--new-window", "--profile", ProfileName}
	if repository != "" {
		args = append(args, repository)
	}
	return args
}

func validateRequest(request Request) error {
	if !localAliasPattern.MatchString(request.Alias) {
		return ErrInvalidAlias
	}
	return nil
}

func (m *Manager) validateRepository(request Request) error {
	if request.Repository == "" {
		return nil
	}
	if m.platform.FileSystem == nil {
		return ErrRepositoryUnavailable
	}
	info, err := m.platform.FileSystem.Stat(request.Repository)
	if err != nil || !info.IsDir() {
		return ErrRepositoryUnavailable
	}
	return nil
}

func (v Version) String() string { return v.Raw }

func mustVersion(value string) Version {
	version, err := ParseVersion(value)
	if err != nil {
		panic(err)
	}
	return version
}

func appendDiagnostic(status *Status, err error) {
	var cliErr *CLIError
	if !errors.As(err, &cliErr) {
		return
	}
	status.Diagnostics = append(status.Diagnostics, Diagnostic{
		Operation: cliErr.Operation, ExitCode: cliErr.ExitCode, Stdout: cliErr.Stdout, Stderr: cliErr.Stderr, OutputTruncated: cliErr.Truncated,
	})
}

func sanitizeDiagnostic(value []byte) string {
	text := string(value)
	if len(text) > maxDiagnosticBytes {
		text = text[:maxDiagnosticBytes]
	}
	for _, pattern := range secretPatterns {
		text = pattern.ReplaceAllString(text, "${1}<redacted>${2}")
	}
	text = unixHomePattern.ReplaceAllString(text, "~")
	text = windowsHomePattern.ReplaceAllString(text, "~")
	return strings.TrimSpace(text)
}

func isProfileCLIError(err error) bool {
	var cliErr *CLIError
	return errors.As(err, &cliErr) && cliErr.Operation == "list Local AI profile extensions"
}
