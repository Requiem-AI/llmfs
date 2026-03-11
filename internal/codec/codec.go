package codec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	VersionTCE1 = "tce1"
	VersionTCE2 = "tce2"
)

var TCE1CodeAlphabet = []string(strings.Split("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz", ""))

type Entry struct {
	Code  string `json:"code"`
	Value string `json:"value"`
}

type Config struct {
	Version string  `json:"version"`
	Escape  string  `json:"escape"`
	Entries []Entry `json:"entries"`
}

type Codec struct {
	version string
	escape  []byte

	encodeRoot *trieNode
	decodeRoot *decodeTrieNode
}

type trieNode struct {
	next map[byte]*trieNode
	code []byte
	leaf bool
}

type decodeTrieNode struct {
	next map[byte]*decodeTrieNode
	val  string
	leaf bool
}

func NewFromConfig(cfg Config) (*Codec, error) {
	if cfg.Version != VersionTCE1 && cfg.Version != VersionTCE2 {
		return nil, fmt.Errorf("unsupported config version %q", cfg.Version)
	}
	if cfg.Escape == "" {
		return nil, errors.New("escape symbol is required")
	}
	return New(cfg.Version, cfg.Escape, cfg.Entries)
}

func New(version string, escape string, entries []Entry) (*Codec, error) {
	if version != VersionTCE1 && version != VersionTCE2 {
		return nil, fmt.Errorf("unsupported version %q", version)
	}
	if escape == "" {
		return nil, errors.New("escape symbol is required")
	}
	escapeBytes := []byte(escape)
	root := &trieNode{next: map[byte]*trieNode{}}
	decodeRoot := &decodeTrieNode{next: map[byte]*decodeTrieNode{}}

	seenVals := map[string]struct{}{}
	seenCodes := map[string]struct{}{}
	for _, e := range entries {
		if e.Code == "" {
			return nil, errors.New("entry code cannot be empty")
		}
		if strings.Contains(e.Value, escape) {
			return nil, fmt.Errorf("entry %q contains escape symbol", e.Value)
		}
		if _, ok := seenVals[e.Value]; ok {
			return nil, fmt.Errorf("duplicate entry value %q", e.Value)
		}
		if _, ok := seenCodes[e.Code]; ok {
			return nil, fmt.Errorf("duplicate code %q", e.Code)
		}
		seenVals[e.Value] = struct{}{}
		seenCodes[e.Code] = struct{}{}

		n := root
		vb := []byte(e.Value)
		for i := 0; i < len(vb); i++ {
			b := vb[i]
			next, ok := n.next[b]
			if !ok {
				next = &trieNode{next: map[byte]*trieNode{}}
				n.next[b] = next
			}
			n = next
		}
		n.leaf = true
		n.code = []byte(e.Code)

		dn := decodeRoot
		cb := []byte(e.Code)
		for i := 0; i < len(cb); i++ {
			b := cb[i]
			next, ok := dn.next[b]
			if !ok {
				next = &decodeTrieNode{next: map[byte]*decodeTrieNode{}}
				dn.next[b] = next
			}
			dn = next
		}
		dn.leaf = true
		dn.val = e.Value
	}

	return &Codec{version: version, escape: escapeBytes, encodeRoot: root, decodeRoot: decodeRoot}, nil
}

func (c *Codec) Encode(input []byte) []byte {
	if c == nil {
		return append([]byte(nil), input...)
	}
	if c.version == VersionTCE2 {
		return c.encodeTCE2(input)
	}
	return c.encodeTCE1(input)
}

func (c *Codec) Decode(input []byte) ([]byte, error) {
	if c == nil {
		return append([]byte(nil), input...), nil
	}
	if c.version == VersionTCE2 {
		return c.decodeTCE2(input)
	}
	return c.decodeTCE1(input)
}

func (c *Codec) encodeTCE1(input []byte) []byte {
	out := bytes.Buffer{}
	for i := 0; i < len(input); {
		if hasPrefixAt(input, i, c.escape) {
			out.Write(c.escape)
			out.Write(c.escape)
			i += len(c.escape)
			continue
		}
		code, width := c.longestValueMatch(input[i:])
		if width > 0 {
			out.Write(c.escape)
			out.Write(code)
			i += width
			continue
		}
		out.WriteByte(input[i])
		i++
	}
	return out.Bytes()
}

func (c *Codec) decodeTCE1(input []byte) ([]byte, error) {
	out := bytes.Buffer{}
	for i := 0; i < len(input); {
		if !hasPrefixAt(input, i, c.escape) {
			out.WriteByte(input[i])
			i++
			continue
		}
		i += len(c.escape)
		if i >= len(input) {
			return nil, errors.New("dangling escape at end of stream")
		}
		if hasPrefixAt(input, i, c.escape) {
			out.Write(c.escape)
			i += len(c.escape)
			continue
		}
		val, width := c.matchCode(input[i:])
		if width == 0 {
			return nil, errors.New("unknown escape code")
		}
		out.WriteString(val)
		i += width
	}
	return out.Bytes(), nil
}

// tce2 uses direct code symbols. Escape is only for literal reserved symbols.
func (c *Codec) encodeTCE2(input []byte) []byte {
	out := bytes.Buffer{}
	for i := 0; i < len(input); {
		if hasPrefixAt(input, i, c.escape) {
			out.Write(c.escape)
			out.Write(c.escape)
			i += len(c.escape)
			continue
		}
		if _, width := c.matchCode(input[i:]); width > 0 {
			out.Write(c.escape)
			out.Write(input[i : i+width])
			i += width
			continue
		}
		code, width := c.longestValueMatch(input[i:])
		if width > 0 {
			out.Write(code)
			i += width
			continue
		}
		out.WriteByte(input[i])
		i++
	}
	return out.Bytes()
}

func (c *Codec) decodeTCE2(input []byte) ([]byte, error) {
	out := bytes.Buffer{}
	for i := 0; i < len(input); {
		if hasPrefixAt(input, i, c.escape) {
			i += len(c.escape)
			if i >= len(input) {
				return nil, errors.New("dangling escape at end of stream")
			}
			_, width := utf8.DecodeRune(input[i:])
			if width <= 0 {
				return nil, errors.New("invalid escaped rune")
			}
			out.Write(input[i : i+width])
			i += width
			continue
		}
		val, width := c.matchCode(input[i:])
		if width > 0 {
			out.WriteString(val)
			i += width
			continue
		}
		out.WriteByte(input[i])
		i++
	}
	return out.Bytes(), nil
}

func (c *Codec) longestValueMatch(input []byte) ([]byte, int) {
	n := c.encodeRoot
	bestW := 0
	var bestCode []byte
	for i := 0; i < len(input); i++ {
		next, ok := n.next[input[i]]
		if !ok {
			break
		}
		n = next
		if n.leaf {
			bestW = i + 1
			bestCode = n.code
		}
	}
	return bestCode, bestW
}

func (c *Codec) matchCode(input []byte) (string, int) {
	n := c.decodeRoot
	bestW := 0
	bestV := ""
	for i := 0; i < len(input); i++ {
		next, ok := n.next[input[i]]
		if !ok {
			break
		}
		n = next
		if n.leaf {
			bestW = i + 1
			bestV = n.val
		}
	}
	return bestV, bestW
}

func hasPrefixAt(b []byte, i int, prefix []byte) bool {
	if i+len(prefix) > len(b) {
		return false
	}
	for j := 0; j < len(prefix); j++ {
		if b[i+j] != prefix[j] {
			return false
		}
	}
	return true
}

func SaveConfig(path string, cfg Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func LoadConfig(path string) (Config, *Codec, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, nil, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, nil, err
	}
	if cfg.Version == "" {
		cfg.Version = VersionTCE1
	}
	if cfg.Escape == "" {
		cfg.Escape = "~"
	}
	c, err := NewFromConfig(cfg)
	if err != nil {
		return Config{}, nil, err
	}
	return cfg, c, nil
}

func BuildConfig(values []string, version string, escape string, codes []string, limit int) Config {
	if version == "" {
		version = VersionTCE1
	}
	if escape == "" {
		escape = "~"
	}
	if len(codes) == 0 {
		codes = TCE1CodeAlphabet
	}
	if limit > len(codes) {
		limit = len(codes)
	}
	if limit > len(values) {
		limit = len(values)
	}
	entries := make([]Entry, 0, limit)
	for i := 0; i < limit; i++ {
		entries = append(entries, Entry{Code: codes[i], Value: values[i]})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Code < entries[j].Code
	})
	return Config{Version: version, Escape: escape, Entries: entries}
}

func BuildConfigFromValues(values []string, limit int) Config {
	return BuildConfig(values, VersionTCE1, "~", TCE1CodeAlphabet, limit)
}
