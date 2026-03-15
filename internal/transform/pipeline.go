package transform

import (
	"fmt"
	"os"
	"runtime/debug"
)

type Stage string

const (
	StageServe  Stage = "serve"
	StageCommit Stage = "commit"
)

type Context struct {
	Path string
	Info os.FileInfo
}

type Result struct {
	Content []byte
	Allowed bool
	Message string
}

type Middleware interface {
	Name() string
	Handle(ctx Context, stage Stage, content []byte) (Result, error)
}

type RejectedError struct {
	Middleware string
	Message    string
}

func (e *RejectedError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("middleware %q rejected content", e.Middleware)
	}
	return fmt.Sprintf("middleware %q rejected content: %s", e.Middleware, e.Message)
}

type Pipeline struct {
	modules []Middleware
}

func NewPipeline(enabledPlugins []string, availablePlugins []Middleware) (*Pipeline, error) {
	availableByName := make(map[string]Middleware, len(availablePlugins))
	for _, plugin := range availablePlugins {
		if plugin == nil {
			continue
		}
		name := plugin.Name()
		if name == "" {
			return nil, fmt.Errorf("available plugin has empty name")
		}
		if _, exists := availableByName[name]; exists {
			return nil, fmt.Errorf("duplicate available plugin %q", name)
		}
		availableByName[name] = plugin
	}

	modules := make([]Middleware, 0, len(enabledPlugins))
	for _, name := range enabledPlugins {
		plugin, ok := availableByName[name]
		if !ok {
			return nil, fmt.Errorf("plugin %q is not available", name)
		}
		modules = append(modules, plugin)
	}

	return &Pipeline{modules: modules}, nil
}

func (p *Pipeline) Serve(ctx Context, content []byte) ([]byte, error) {
	return p.run(ctx, StageServe, content, false)
}

func (p *Pipeline) Commit(ctx Context, content []byte) ([]byte, error) {
	return p.run(ctx, StageCommit, content, true)
}

func (p *Pipeline) run(ctx Context, stage Stage, content []byte, reverse bool) ([]byte, error) {
	current := append([]byte(nil), content...)
	start, end, step := 0, len(p.modules), 1
	if reverse {
		start, end, step = len(p.modules)-1, -1, -1
	}
	for i := start; i != end; i += step {
		m := p.modules[i]
		result, err := handleSafely(m, ctx, stage, current)
		if err != nil {
			return nil, fmt.Errorf("middleware %q: %w", m.Name(), err)
		}
		if !result.Allowed {
			return nil, &RejectedError{Middleware: m.Name(), Message: result.Message}
		}
		current = result.Content
	}
	return current, nil
}

func handleSafely(m Middleware, ctx Context, stage Stage, content []byte) (result Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic recovered: %v\n%s", r, debug.Stack())
		}
	}()
	return m.Handle(ctx, stage, content)
}
