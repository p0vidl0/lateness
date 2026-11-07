package main

import (
	"fmt"
	"os"

	"github.com/p0vidl0/lateness/internal/envrender"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: render-env <template.env> <out.env>")
		os.Exit(2)
	}
	name := os.Getenv("COMPOSE_PROJECT_NAME")
	if name == "" {
		fmt.Fprintln(os.Stderr, "render-env: COMPOSE_PROJECT_NAME is required")
		os.Exit(1)
	}
	if err := envrender.WriteGenerated(os.Args[1], os.Args[2], name); err != nil {
		fmt.Fprintf(os.Stderr, "render-env: %s\n", err)
		os.Exit(1)
	}
}
