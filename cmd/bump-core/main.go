package main

import (
	"fmt"
	"os"

	"github.com/MilosRandelovic/bump-core/v2/internal/protocol"
	"github.com/MilosRandelovic/bump-core/v2/shared"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println(shared.Version)
		os.Exit(0)
	}

	server := protocol.NewServer()
	if err := server.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
