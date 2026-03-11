package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"llmfs/internal/codec"
	"llmfs/internal/explorer"
	"llmfs/internal/mountfs"
)

const (
	defaultConfigPath = ".llmfs/config.json"
	defaultInitPath   = ".llmfs/INIT_INSTRUCTIONS.md"
)

var version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "explore":
		must(runExplore(os.Args[2:], false))
	case "init":
		must(runExplore(os.Args[2:], true))
	case "mount":
		must(runMount(os.Args[2:]))
	case "run":
		must(runMount(os.Args[2:]))
	case "version":
		fmt.Println(version)
	case "encode":
		must(runEncodeDecode(os.Args[2:], true))
	case "decode":
		must(runEncodeDecode(os.Args[2:], false))
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `llmfs: token-optimized LLM filesystem bridge

Usage:
  llmfs explore [--root DIR]
  llmfs init [--root DIR] [--config PATH] [--instructions PATH]
  llmfs mount --mountpoint DIR [--root DIR] [--config PATH]
  llmfs run --mountpoint DIR [--root DIR] [--config PATH]
  llmfs version
  llmfs encode [--config PATH] [--in FILE] [--out FILE]
  llmfs decode [--config PATH] [--in FILE] [--out FILE]
`)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runExplore(args []string, writeFiles bool) error {
	fs := flag.NewFlagSet("explore", flag.ContinueOnError)
	root := fs.String("root", ".", "repository root")
	dictSize := fs.Int("dict-size", 0, "dictionary size (advanced; 0 = automatic)")
	format := fs.String("format", codec.VersionTCE2, "transport format: tce1 or tce2")
	tokenizer := fs.String("tokenizer", "cl100k_base", "tokenizer name for optimization (advanced)")
	configPath := fs.String("config", defaultConfigPath, "config output path")
	instructionsPath := fs.String("instructions", defaultInitPath, "LLM init instructions output path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	result, err := explorer.BuildDictionary(explorer.Options{
		Root:      *root,
		DictSize:  *dictSize,
		Format:    *format,
		Tokenizer: *tokenizer,
	})
	if err != nil {
		return err
	}
	printResult(result)
	if !writeFiles {
		return nil
	}
	cfg := codec.BuildConfig(result.Entries, result.Version, result.Escape, result.Codes, len(result.Entries))
	if err := codec.SaveConfig(*configPath, cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*instructionsPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(*instructionsPath, []byte(buildInstructions(cfg)), 0o644); err != nil {
		return err
	}
	fmt.Printf("\nWrote config: %s\nWrote instructions: %s\n", *configPath, *instructionsPath)
	return nil
}

func printResult(result explorer.Result) {
	fmt.Printf("Root: %s\n", result.Root)
	fmt.Printf("Format: %s\n", result.Version)
	fmt.Printf("Files scanned: %d\n", result.FilesScanned)
	fmt.Printf("Bytes scanned: %d\n", result.BytesScanned)
	fmt.Printf("Raw tokens: %d\n", result.RawTokens)
	fmt.Printf("Encoded tokens: %d\n", result.EncodedTokens)
	fmt.Printf("Token reduction: %.2f%%\n", result.TokenReduction)
	fmt.Printf("Escape symbol: %s\n", symbolLabel(result.Escape))
	fmt.Println("Dictionary candidates:")
	for i, e := range result.Entries {
		if i >= len(result.Codes) {
			break
		}
		fmt.Printf("  %s => %q\n", symbolLabel(result.Codes[i]), e)
	}
}

func runMount(args []string) error {
	fs := flag.NewFlagSet("mount", flag.ContinueOnError)
	root := fs.String("root", ".", "repository root")
	mountpoint := fs.String("mountpoint", "", "mount destination (required)")
	configPath := fs.String("config", defaultConfigPath, "config path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*mountpoint) == "" {
		return fmt.Errorf("--mountpoint is required")
	}
	if err := ensureConfig(*root, *configPath); err != nil {
		return err
	}
	fmt.Printf("Mounting encoded view of %s at %s\n", *root, *mountpoint)
	return mountfs.Mount(*root, *mountpoint, *configPath)
}

func ensureConfig(root, configPath string) error {
	if _, err := os.Stat(configPath); err == nil {
		return nil
	}
	fmt.Printf("Config not found at %s, generating defaults (tce2)...\n", configPath)
	result, err := explorer.BuildDictionary(explorer.Options{
		Root:      root,
		Format:    codec.VersionTCE2,
		Tokenizer: "cl100k_base",
		DictSize:  0,
	})
	if err != nil {
		return err
	}
	cfg := codec.BuildConfig(result.Entries, result.Version, result.Escape, result.Codes, len(result.Entries))
	if err := codec.SaveConfig(configPath, cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(defaultInitPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(defaultInitPath, []byte(buildInstructions(cfg)), 0o644); err != nil {
		return err
	}
	fmt.Printf("Wrote config: %s\nWrote instructions: %s\n", configPath, defaultInitPath)
	return nil
}

func runEncodeDecode(args []string, encode bool) error {
	name := "decode"
	if encode {
		name = "encode"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath, "config path")
	inPath := fs.String("in", "", "input file (default stdin)")
	outPath := fs.String("out", "", "output file (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, cdc, err := codec.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	input, err := readInput(*inPath)
	if err != nil {
		return err
	}
	var out []byte
	if encode {
		out = cdc.Encode(input)
	} else {
		out, err = cdc.Decode(input)
		if err != nil {
			return err
		}
	}
	return writeOutput(*outPath, out)
}

func readInput(path string) ([]byte, error) {
	if path == "" {
		return os.ReadFile("/dev/stdin")
	}
	return os.ReadFile(path)
}

func writeOutput(path string, b []byte) error {
	if path == "" {
		_, err := os.Stdout.Write(b)
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func buildInstructions(cfg codec.Config) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# LLM Transport Instructions (%s)\n\n", cfg.Version))
	b.WriteString("Use this reversible transport format for repository text payloads.\n")
	b.WriteString("Rules:\n")
	if cfg.Version == codec.VersionTCE2 {
		b.WriteString(fmt.Sprintf("1. A code symbol directly expands to its dictionary entry. Escape symbol is %s.\n", symbolLabel(cfg.Escape)))
		b.WriteString("2. `ESC + symbol` emits that symbol literally (used for escape or code symbols).\n")
	} else {
		b.WriteString(fmt.Sprintf("1. `ESC+CODE` means expand to dictionary entry (ESC = %s).\n", symbolLabel(cfg.Escape)))
		b.WriteString("2. `ESC+ESC` means a literal escape symbol.\n")
	}
	b.WriteString("3. When emitting encoded content, always use longest-match replacement first.\n")
	b.WriteString("4. Treat payload as byte-preserving text; do not normalize whitespace.\n\n")
	b.WriteString("Dictionary:\n")
	for _, e := range cfg.Entries {
		b.WriteString(fmt.Sprintf("- `%s` => `%s`\n", symbolLabel(e.Code), escapeBackticks(e.Value)))
	}
	return b.String()
}

func symbolLabel(s string) string {
	r := []rune(s)
	if len(r) == 1 {
		if r[0] >= 32 && r[0] <= 126 {
			return string(r[0])
		}
		return fmt.Sprintf("U+%04X", r[0])
	}
	return fmt.Sprintf("%q", s)
}

func escapeBackticks(s string) string {
	s = strings.ReplaceAll(s, "`", "\\`")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}
