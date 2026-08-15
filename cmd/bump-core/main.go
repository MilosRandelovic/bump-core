package main

import (
	"fmt"
	"os"

	"github.com/MilosRandelovic/bump-core/internal/protocol"
	"github.com/MilosRandelovic/bump-core/shared"
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
