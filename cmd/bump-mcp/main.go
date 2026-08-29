package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/MilosRandelovic/bump-core/v2/internal/mcp"
	"github.com/MilosRandelovic/bump-core/v2/shared"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Printf("bump-mcp version %s\n", shared.Version)
		return
	}

	if err := mcp.NewServer().Run(context.Background()); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
