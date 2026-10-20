package ollama

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/platform"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/state"
	"github.com/artofthesmart/dotfiles-bootstrap/internal/testkit"
)

func TestEnsureServerStartsManagerProcessWithRequiredContext(t *testing.T) {
	api := newCompatibleAPI()
	api.statusErr = errors.New("not ready")
	api.statuses = []error{
		errors.New("not ready"),
		errors.New("not ready"),
		nil,
	}
	processes := &testkit.ScriptedProcessManager{
		Starts: []testkit.ProcessStart{{
			State: platform.ProcessState{PID: 42, Running: true},
		}},
	}
	manager := Manager{
		API:       api,
		Processes: processes,
		Sleep:     func(context.Context, time.Duration) error { return nil },
	}
	api.statusErr = nil
	state, err := manager.EnsureServer(context.Background(), ServerStartOptions{
		AllowStart: true,
		Attempts:   3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Ready || !state.StartedProcess || state.PID != 42 {
		t.Fatalf("server state = %#v", state)
	}
	calls := processes.StartedCommands()
	if len(calls) != 1 || calls[0].Name != "ollama" || len(calls[0].Args) != 1 || calls[0].Args[0] != "serve" {
		t.Fatalf("process calls = %#v", calls)
	}
	if len(calls[0].Env) != 1 || calls[0].Env[0] != "OLLAMA_CONTEXT_LENGTH=65536" {
		t.Fatalf("server environment = %#v", calls[0].Env)
	}
}

func TestEnsureServerRequiresConsentBeforeExistingServiceRestart(t *testing.T) {
	api := newCompatibleAPI()
	api.statusErr = errors.New("not ready")
	service := &testkit.ScriptedService{State: platform.ServiceRunning}
	manager := Manager{API: api, Services: service}
	_, err := manager.EnsureServer(context.Background(), ServerStartOptions{AllowStart: true})
	var operationError *Error
	if !errors.As(err, &operationError) || operationError.Kind != ErrorConsent {
		t.Fatalf("EnsureServer() error = %#v, want restart consent", err)
	}
	if service.StopCalls != 0 || service.StartCalls != 0 {
		t.Fatalf("service restarted without consent: stop=%d start=%d", service.StopCalls, service.StartCalls)
	}
}

func TestRepairPlanNeverOffersDriverCloudOrLowerContext(t *testing.T) {
	diagnosis := Diagnosis{Issues: []state.Issue{
		runtimeIssue(IssueContextFailed, state.SeverityError, "Context failed.", "repair.ollama.restart", "Restart.", false),
		modelIssue(IssueModelRemote, "Remote model rejected.", "repair.ollama.model", "Replace model."),
	}}
	plan := PlanRepair(diagnosis, true)
	if len(plan.Actions) != 2 {
		t.Fatalf("repair actions = %#v", plan.Actions)
	}
	for _, action := range plan.Actions {
		lower := strings.ToLower(action.Summary)
		if strings.Contains(lower, "driver") || strings.Contains(lower, "cloud fallback") || strings.Contains(lower, "lower context") {
			t.Fatalf("unsafe repair action = %#v", action)
		}
		if action.ID == "repair.ollama.restart" && (!action.ConsentRequired || action.SafeForYes) {
			t.Fatalf("restart action does not require explicit consent: %#v", action)
		}
	}
}

func TestDiagnoseUsesStableToolContextAndRemoteCodes(t *testing.T) {
	alias := "local-ai-test:latest"
	tests := []struct {
		name      string
		configure func(*fakeRuntimeAPI)
		want      state.IssueCode
	}{
		{
			name: "tools",
			configure: func(api *fakeRuntimeAPI) {
				api.chat = ChatResponse{Done: true, Message: ChatMessage{Content: `{"tool":"prose only"}`}}
			},
			want: IssueToolsFailed,
		},
		{
			name: "context",
			configure: func(api *fakeRuntimeAPI) {
				api.ps = PSResponse{Models: []RunningModel{{Name: alias, ContextLength: 32768}}}
			},
			want: IssueContextFailed,
		},
		{
			name: "remote",
			configure: func(api *fakeRuntimeAPI) {
				show := compatibleShow(131072)
				show.RemoteModel = "remote"
				api.addModel(alias, show, 5<<30)
			},
			want: IssueModelRemote,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := newCompatibleAPI()
			api.addModel(alias, compatibleShow(131072), 5<<30)
			api.ps = PSResponse{Models: []RunningModel{{Name: alias, ContextLength: 65536, Processor: "100% GPU"}}}
			test.configure(api)
			diagnosis := (Manager{API: api}).Diagnose(context.Background(), alias)
			if len(diagnosis.Issues) != 1 || diagnosis.Issues[0].Code != test.want {
				t.Fatalf("diagnosis issues = %#v, want %s", diagnosis.Issues, test.want)
			}
		})
	}
}
