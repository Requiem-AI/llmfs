package codec

import (
	"testing"

	internalcodec "llmfs/internal/codec"
	"llmfs/internal/transform"
)

func TestCodecMiddlewareRoundTrip(t *testing.T) {
	cdc, err := internalcodec.New(internalcodec.VersionTCE2, "~", []internalcodec.Entry{{Code: "@", Value: "the"}})
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}
	m := New(cdc)

	served, err := m.Handle(transform.Context{}, transform.StageServe, []byte("the value"))
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	committed, err := m.Handle(transform.Context{}, transform.StageCommit, served.Content)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	if string(committed.Content) != "the value" {
		t.Fatalf("unexpected round trip: %q", string(committed.Content))
	}
}
