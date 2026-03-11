package transform

import (
	"fmt"

	"llmfs/internal/codec"
)

func NewDefaultRegistry(cdc *codec.Codec) *Registry {
	r := NewRegistry()
	r.Register("codec", func(_ map[string]any) (Middleware, error) {
		if cdc == nil {
			return nil, fmt.Errorf("codec middleware requires a codec instance")
		}
		return NewCodecMiddleware(cdc), nil
	})
	r.Register("deny_binary", func(_ map[string]any) (Middleware, error) {
		return NewDenyBinaryMiddleware(), nil
	})
	r.Register("deny_env_dotfiles", func(_ map[string]any) (Middleware, error) {
		return NewDenyEnvDotfilesMiddleware(), nil
	})
	r.Register("redirect_env_to_agent", func(_ map[string]any) (Middleware, error) {
		return NewRedirectEnvToAgentMiddleware(), nil
	})
	return r
}
