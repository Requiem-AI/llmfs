package harness

import (
	"os"

	commonctx "github.com/alphabatem/common/context"

	"llmfs/internal/app"
)

const CLIServiceID = "llmfs.cli"

type CLIService struct {
	commonctx.DefaultService
	Args    []string
	Version string
}

func (s *CLIService) Id() string {
	return CLIServiceID
}

func (s *CLIService) Configure(ctx *commonctx.Context) error {
	if err := s.DefaultService.Configure(ctx); err != nil {
		return err
	}
	if len(s.Args) == 0 {
		s.Args = os.Args
	}
	if s.Version == "" {
		s.Version = "dev"
	}
	return nil
}

func (s *CLIService) Start() error {
	return app.Run(s.Args, s.Version)
}

func (s *CLIService) Shutdown() {
	// no-op: CLI commands are process-scoped and do not hold long-lived resources
}
