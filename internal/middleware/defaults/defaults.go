package defaults

import (
	"fmt"

	internalcodec "llmfs/internal/codec"
	"llmfs/internal/middleware/codec"
	"llmfs/internal/middleware/denybinary"
	"llmfs/internal/middleware/denyenvdotfiles"
	"llmfs/internal/middleware/redirectenvtoagent"
	"llmfs/internal/transform"
)

func NewRegistry(cdc *internalcodec.Codec) *transform.Registry {
	r := transform.NewRegistry()
	r.Register("codec", func(_ map[string]any) (transform.Middleware, error) {
		if cdc == nil {
			return nil, fmt.Errorf("codec middleware requires a codec instance")
		}
		return codec.New(cdc), nil
	})
	r.Register("deny_binary", func(_ map[string]any) (transform.Middleware, error) {
		return denybinary.New(), nil
	})
	r.Register("deny_env_dotfiles", func(_ map[string]any) (transform.Middleware, error) {
		return denyenvdotfiles.New(), nil
	})
	r.Register("redirect_env_to_agent", func(_ map[string]any) (transform.Middleware, error) {
		return redirectenvtoagent.New(), nil
	})
	return r
}
