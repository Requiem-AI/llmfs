package codec

import "testing"

func TestRoundTripTCE1(t *testing.T) {
	cfg := BuildConfig([]string{"package ", "func ", "return ", "\n\t"}, VersionTCE1, "~", TCE1CodeAlphabet, 4)
	c, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}
	in := []byte("package main\n\nfunc x() string {\n\treturn \"~ok\"\n}\n")
	enc := c.Encode(in)
	out, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(out) != string(in) {
		t.Fatalf("mismatch\nwant: %q\ngot:  %q", in, out)
	}
}

func TestRoundTripTCE2(t *testing.T) {
	codes := []string{"\uE001", "\uE002", "\uE003"}
	cfg := BuildConfig([]string{"package ", "func ", "return "}, VersionTCE2, "\uE000", codes, 3)
	c, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}
	in := []byte("package main\nfunc x() int { return 1 }\n")
	enc := c.Encode(in)
	out, err := c.Decode(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(out) != string(in) {
		t.Fatalf("mismatch\nwant: %q\ngot:  %q", in, out)
	}
}

func TestDecodeUnknownEscape(t *testing.T) {
	cfg := BuildConfig([]string{"package "}, VersionTCE1, "~", TCE1CodeAlphabet, 1)
	c, err := NewFromConfig(cfg)
	if err != nil {
		t.Fatalf("new codec: %v", err)
	}
	if _, err := c.Decode([]byte("~Z")); err == nil {
		t.Fatal("expected decode error")
	}
}
