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

type ModuleConfig struct {
	Name    string
	Enabled bool
	Options map[string]any
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

func NewPipeline(configs []ModuleConfig, r *Registry) (*Pipeline, error) {
	if r == nil {
		return nil, fmt.Errorf("middleware registry is nil")
	}
	modules := make([]Middleware, 0, len(configs))
	for _, cfg := range configs {
		if !cfg.Enabled {
			continue
		}
		m, err := r.Create(cfg.Name, cfg.Options)
		if err != nil {
			return nil, err
		}
		modules = append(modules, m)
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
	if !reverse {
		for _, m := range p.modules {
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
	for i := len(p.modules) - 1; i >= 0; i-- {
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
