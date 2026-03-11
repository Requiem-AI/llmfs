package transform

import "llmfs/internal/codec"

type codecMiddleware struct {
	cdc *codec.Codec
}

func NewCodecMiddleware(cdc *codec.Codec) Middleware {
	return &codecMiddleware{cdc: cdc}
}

func (m *codecMiddleware) Name() string {
	return "codec"
}

func (m *codecMiddleware) Handle(_ Context, stage Stage, content []byte) (Result, error) {
	if stage == StageServe {
		return Result{Content: m.cdc.Encode(content), Allowed: true}, nil
	}
	decoded, err := m.cdc.Decode(content)
	if err != nil {
		return Result{}, err
	}
	return Result{Content: decoded, Allowed: true}, nil
}
