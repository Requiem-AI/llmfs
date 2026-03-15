package main

import (
	"fmt"
	"os"

	commonctx "github.com/alphabatem/common/context"

	"llmfs/internal/harness"
)

var version = "dev"

func main() {
	ctx, err := commonctx.NewCtx(&harness.CLIService{
		Args:    os.Args,
		Version: version,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if err := ctx.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
