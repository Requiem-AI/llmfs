package transform

import "fmt"

type Factory func(options map[string]any) (Middleware, error)

type Registry struct {
	factories map[string]Factory
}

func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

func (r *Registry) Register(name string, f Factory) {
	r.factories[name] = f
}

func (r *Registry) Create(name string, options map[string]any) (Middleware, error) {
	f, ok := r.factories[name]
	if !ok {
		return nil, fmt.Errorf("unknown middleware %q", name)
	}
	return f(options)
}
