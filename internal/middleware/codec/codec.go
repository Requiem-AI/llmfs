package codec

import (
	internalcodec "llmfs/internal/codec"
	"llmfs/internal/transform"
)

type middleware struct {
	cdc *internalcodec.Codec
}

func New(cdc *internalcodec.Codec) transform.Middleware {
	return &middleware{cdc: cdc}
}

func (m *middleware) Name() string {
	return "codec"
}

func (m *middleware) Handle(_ transform.Context, stage transform.Stage, content []byte) (transform.Result, error) {
	if stage == transform.StageServe {
		return transform.Result{Content: m.cdc.Encode(content), Allowed: true}, nil
	}
	decoded, err := m.cdc.Decode(content)
	if err != nil {
		return transform.Result{}, err
	}
	return transform.Result{Content: decoded, Allowed: true}, nil
}
