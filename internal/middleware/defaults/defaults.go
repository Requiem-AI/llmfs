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

func AvailablePlugins(cdc *internalcodec.Codec) ([]transform.Middleware, error) {
	if cdc == nil {
		return nil, fmt.Errorf("codec plugin requires a codec instance")
	}
	return []transform.Middleware{
		denyenvdotfiles.New(),
		redirectenvtoagent.New(),
		codec.New(cdc),
		denybinary.New(),
	}, nil
}
