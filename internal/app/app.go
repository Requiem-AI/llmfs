package app

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"llmfs/internal/appcfg"
	"llmfs/internal/codec"
	"llmfs/internal/explorer"
	"llmfs/internal/middleware/defaults"
	"llmfs/internal/mountfs"
	"llmfs/internal/setup"
	"llmfs/internal/transform"
)

func Run(args []string, version string) error {
	if len(args) < 2 {
		return runMount([]string{})
	}

	switch args[1] {
	case "explore":
		return runExplore(args[2:], false)
	case "init":
		return runExplore(args[2:], true)
	case "mount", "run":
		return runMount(args[2:])
	case "version":
		fmt.Println(version)
		return nil
	case "encode":
		return runEncodeDecode(args[2:], true)
	case "decode":
		return runEncodeDecode(args[2:], false)
	default:
		usage()
		return fmt.Errorf("unknown command: %s", args[1])
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `llmfs: token-optimized LLM filesystem bridge

Usage:
  llmfs                             (same as: llmfs run, default mountpoint .llmfs/mount)
  llmfs explore [-v] [--root DIR] [--settings PATH]
  llmfs init [-v] [--root DIR] [--config PATH] [--instructions PATH] [--settings PATH]
  llmfs mount [-v] [--mountpoint DIR] [--root DIR] [--config PATH] [--settings PATH]
  llmfs run [-v] [--mountpoint DIR] [--root DIR] [--config PATH] [--settings PATH]
  llmfs version
  llmfs encode [--config PATH] [--in FILE] [--out FILE]
  llmfs decode [--config PATH] [--in FILE] [--out FILE]
`)
}

func runExplore(args []string, writeFiles bool) error {
	fs := flag.NewFlagSet("explore", flag.ContinueOnError)
	root := fs.String("root", ".", "repository root")
	verbose := fs.Bool("v", false, "verbose progress (print each scanned file)")
	dictSize := fs.Int("dict-size", 0, "dictionary size (advanced; 0 = automatic)")
	format := fs.String("format", codec.VersionTCE2, "transport format: tce1 or tce2")
	tokenizer := fs.String("tokenizer", "cl100k_base", "tokenizer name for optimization (advanced)")
	configPath := fs.String("config", setup.DefaultConfigPath, "config output path")
	instructionsPath := fs.String("instructions", setup.DefaultInitPath, "LLM init instructions output path")
	settingsPath := fs.String("settings", setup.DefaultSettings, "settings file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	settings, err := setup.LoadSettings(*root, *settingsPath)
	if err != nil {
		return err
	}
	lastStatus := ""
	result, err := explorer.BuildDictionary(explorer.Options{
		Root:      *root,
		DictSize:  *dictSize,
		Format:    *format,
		Tokenizer: *tokenizer,
		Verbose:   *verbose,
		Settings:  settings,
		Progress:  makeProgressPrinter(&lastStatus),
	})
	if err != nil {
		return err
	}
	printResult(result)
	if !writeFiles {
		return nil
	}
	cfg := codec.BuildConfig(result.Entries, result.Version, result.Escape, result.Codes, len(result.Entries))
	if err := writeGeneratedFiles(*configPath, *instructionsPath, cfg); err != nil {
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
	verbose := fs.Bool("v", false, "verbose progress (print each scanned file)")
	mountpoint := fs.String("mountpoint", setup.DefaultMountpoint, "mount destination")
	configPath := fs.String("config", setup.DefaultConfigPath, "config path")
	settingsPath := fs.String("settings", setup.DefaultSettings, "settings file path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	*mountpoint = setup.NormalizeMountpoint(*mountpoint)
	settings, err := setup.LoadSettings(*root, *settingsPath)
	if err != nil {
		return err
	}
	if err := ensureConfig(*root, *configPath, settings, *verbose); err != nil {
		return err
	}
	fmt.Printf("Mounting encoded view of %s at %s\n", *root, *mountpoint)
	return mountfs.Mount(*root, *mountpoint, *configPath, settings)
}

func ensureConfig(root, configPath string, settings appcfg.Settings, verbose bool) error {
	if _, err := os.Stat(configPath); err == nil {
		return nil
	}
	fmt.Printf("Config not found at %s, generating defaults (tce2)\n", configPath)
	fmt.Printf("Preparing dictionary from %s\n", root)
	start := time.Now()
	lastStatus := ""
	result, err := explorer.BuildDictionary(explorer.Options{
		Root:      root,
		Format:    codec.VersionTCE2,
		Tokenizer: "cl100k_base",
		DictSize:  0,
		Verbose:   verbose,
		Settings:  settings,
		Progress:  makeProgressPrinter(&lastStatus),
	})
	if err != nil {
		return err
	}
	cfg := codec.BuildConfig(result.Entries, result.Version, result.Escape, result.Codes, len(result.Entries))
	if err := writeGeneratedFiles(configPath, setup.DefaultInitPath, cfg); err != nil {
		return err
	}
	fmt.Printf("Wrote config: %s\nWrote instructions: %s\n", configPath, setup.DefaultInitPath)
	fmt.Printf("Dictionary ready in %s\n", time.Since(start).Round(time.Second))
	return nil
}

func makeProgressPrinter(lastStatus *string) func(explorer.Progress) {
	return func(p explorer.Progress) {
		statusKey := fmt.Sprintf("%s|%s|%d|%d|%d|%d|%s", p.Phase, p.Message, p.FilesScanned, p.BytesScanned, p.Current, p.Total, p.Path)
		if statusKey == *lastStatus {
			return
		}
		*lastStatus = statusKey
		progress := ""
		if p.Total > 0 {
			progress = fmt.Sprintf(" [%d/%d]", p.Current, p.Total)
		}
		if p.Path != "" {
			fmt.Printf("  [%s] %s%s %s (%d files, %s)\n", p.Phase, p.Message, progress, p.Path, p.FilesScanned, humanBytes(p.BytesScanned))
			return
		}
		switch p.Phase {
		case "scan", "evaluate", "analyze", "score":
			fmt.Printf("  [%s] %s%s (%d files, %s)\n", p.Phase, p.Message, progress, p.FilesScanned, humanBytes(p.BytesScanned))
		default:
			fmt.Printf("  [%s] %s\n", p.Phase, p.Message)
		}
	}
}

func writeGeneratedFiles(configPath, instructionsPath string, cfg codec.Config) error {
	if err := codec.SaveConfig(configPath, cfg); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(instructionsPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(instructionsPath, []byte(buildInstructions(cfg)), 0o644); err != nil {
		return err
	}
	return nil
}

func humanBytes(n int) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := unit, 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func runEncodeDecode(args []string, encode bool) error {
	name := "decode"
	if encode {
		name = "encode"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	configPath := fs.String("config", setup.DefaultConfigPath, "config path")
	inPath := fs.String("in", "", "input file (default stdin)")
	outPath := fs.String("out", "", "output file (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, cdc, err := codec.LoadConfig(*configPath)
	if err != nil {
		return err
	}
	availablePlugins, err := defaults.AvailablePlugins(cdc)
	if err != nil {
		return err
	}
	pipeline, err := transform.NewPipeline([]string{"codec"}, availablePlugins)
	if err != nil {
		return err
	}
	input, err := readInput(*inPath)
	if err != nil {
		return err
	}
	ctx := transform.Context{Path: *inPath}
	var out []byte
	if encode {
		out, err = pipeline.Serve(ctx, input)
	} else {
		out, err = pipeline.Commit(ctx, input)
	}
	if err != nil {
		return err
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
