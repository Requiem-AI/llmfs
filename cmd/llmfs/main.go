package main

import (
	"fmt"
	"os"

	"llmfs/internal/app"
)

var version = "dev"

func main() {
	if err := app.Run(os.Args, version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
