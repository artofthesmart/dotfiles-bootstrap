package ui

import (
	"fmt"
	"io"
	"sort"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
)

func RenderResult(w io.Writer, result app.Result) {
	fmt.Fprintf(w, "Local AI Manager — %s\n", result.Summary)
	fmt.Fprintf(w, "Outcome: %s (exit %d)\n", result.Outcome, result.ExitCode)
	if result.TransactionID != nil {
		fmt.Fprintf(w, "Transaction: %s\n", *result.TransactionID)
	}
	if len(result.Components) > 0 {
		fmt.Fprintln(w, "\nComponents:")
		for _, component := range result.Components {
			verification := "not verified"
			if component.Verified {
				verification = "verified"
			}
			fmt.Fprintf(w, "  - %s: %s (%s)\n", component.Component, component.State, verification)
			keys := make([]string, 0, len(component.Attributes))
			for key := range component.Attributes {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				fmt.Fprintf(w, "      %s: %v\n", key, component.Attributes[key])
			}
		}
	}
	if len(result.Issues) > 0 {
		fmt.Fprintln(w, "\nIssues:")
	}
	for _, issue := range result.Issues {
		fmt.Fprintf(w, "  - %s [%s]: %s\n", issue.Code, issue.Severity, issue.Summary)
		for _, remediation := range issue.Remediations {
			fmt.Fprintf(w, "      %s: %s\n", remediation.Kind, remediation.Summary)
		}
	}
	if len(result.ManualActions) > 0 {
		fmt.Fprintln(w, "\nManual actions:")
		for _, action := range result.ManualActions {
			fmt.Fprintf(w, "  - %s (%s): %s\n", action.ActionID, action.Surface, action.Summary)
		}
	}
}
