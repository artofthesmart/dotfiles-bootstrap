package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/artofthesmart/dotfiles-bootstrap/internal/catalog"
)

func main() {
	timeout := flag.Duration("timeout", 3*time.Minute, "overall read-only registry check timeout")
	pretty := flag.Bool("pretty", false, "indent the JSON report")
	flag.Parse()

	value, err := catalog.LoadEmbedded()
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	report, err := (catalog.DriftChecker{}).Check(ctx, value)
	if err != nil {
		fatal(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	if *pretty {
		encoder.SetIndent("", "  ")
	}
	if err := encoder.Encode(report); err != nil {
		fatal(err)
	}
	if !report.Clean {
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "catalog drift check: %v\n", err)
	os.Exit(2)
}
