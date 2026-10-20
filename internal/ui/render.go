package ui

import (
	"fmt"
	"io"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/app"
)

func RenderResult(w io.Writer, result app.Result) {
	fmt.Fprintf(w, "local-ai: %s (exit %d)\n", result.Summary, result.ExitCode)
	for _, issue := range result.Issues {
		fmt.Fprintf(w, "  %s: %s\n", issue.Code, issue.Summary)
	}
}
