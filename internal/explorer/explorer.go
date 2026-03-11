package explorer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pkoukk/tiktoken-go"

	"llmfs/internal/appcfg"
	"llmfs/internal/codec"
)

var wordRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{3,}`)

type Result struct {
	Root           string
	FilesScanned   int
	BytesScanned   int
	RawTokens      int
	EncodedTokens  int
	TokenReduction float64
	Entries        []string
	Version        string
	Escape         string
	Codes          []string
}

type Options struct {
	Root         string
	DictSize     int
	MaxFiles     int
	MaxTotalSize int
	Format       string
	Tokenizer    string
	Settings     appcfg.Settings
	Progress     func(Progress)
}

type Progress struct {
	Phase        string
	Message      string
	FilesScanned int
	BytesScanned int
	Current      int
	Total        int
}

func BuildDictionary(opts Options) (Result, error) {
	reportProgress(opts, Progress{Phase: "setup", Message: "Preparing dictionary build"})
	if opts.DictSize <= 0 {
		if opts.Format == codec.VersionTCE1 {
			opts.DictSize = 62
		} else {
			opts.DictSize = 96
		}
	}
	if opts.MaxFiles <= 0 {
		opts.MaxFiles = 5000
	}
	if opts.MaxTotalSize <= 0 {
		opts.MaxTotalSize = 8 << 20
	}
	if opts.Format == "" {
		opts.Format = codec.VersionTCE1
	}
	if opts.Tokenizer == "" {
		opts.Tokenizer = "cl100k_base"
	}
	if len(opts.Settings.Candidates) == 0 {
		opts.Settings = appcfg.DefaultSettings()
	}

	tk, err := tiktoken.GetEncoding(opts.Tokenizer)
	if err != nil {
		return Result{}, fmt.Errorf("load tokenizer %q: %w", opts.Tokenizer, err)
	}
	reportProgress(opts, Progress{Phase: "scan", Message: "Scanning repository files"})

	files := make([][]byte, 0, 256)
	totalBytes := 0
	lastScanProgress := time.Now()
	err = filepath.WalkDir(opts.Root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if shouldSkipDir(d.Name(), opts.Settings.SkipDirs) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) >= opts.MaxFiles || totalBytes >= opts.MaxTotalSize {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if !utf8.Valid(b) || len(b) == 0 {
			return nil
		}
		totalBytes += len(b)
		files = append(files, b)
		if len(files)%100 == 0 || time.Since(lastScanProgress) >= 2*time.Second {
			reportProgress(opts, Progress{
				Phase:        "scan",
				Message:      "Reading files",
				FilesScanned: len(files),
				BytesScanned: totalBytes,
			})
			lastScanProgress = time.Now()
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	reportProgress(opts, Progress{
		Phase:        "scan",
		Message:      "Repository scan complete",
		FilesScanned: len(files),
		BytesScanned: totalBytes,
	})
	if len(files) == 0 {
		return Result{}, fmt.Errorf("no eligible UTF-8 files found under %s", opts.Root)
	}

	reportProgress(opts, Progress{
		Phase:        "analyze",
		Message:      "Collecting candidate dictionary entries",
		FilesScanned: len(files),
		BytesScanned: totalBytes,
	})
	candidates := make(map[string]struct{}, 4000)
	for _, c := range opts.Settings.Candidates {
		candidates[c] = struct{}{}
	}
	wordFreq := map[string]int{}
	lastAnalyzeProgress := time.Now()
	for i, b := range files {
		for _, w := range wordRe.FindAllString(string(b), -1) {
			wordFreq[w]++
		}
		if (i+1)%10 == 0 || time.Since(lastAnalyzeProgress) >= 2*time.Second {
			reportProgress(opts, Progress{
				Phase:        "analyze",
				Message:      "Extracting frequent words",
				FilesScanned: i + 1,
				BytesScanned: totalBytes,
				Current:      i + 1,
				Total:        len(files),
			})
			lastAnalyzeProgress = time.Now()
		}
	}
	type wf struct {
		w string
		n int
	}
	words := make([]wf, 0, len(wordFreq))
	for w, n := range wordFreq {
		if n >= 3 {
			words = append(words, wf{w: w, n: n})
		}
	}
	sort.Slice(words, func(i, j int) bool {
		if words[i].n == words[j].n {
			return words[i].w < words[j].w
		}
		return words[i].n > words[j].n
	})
	if len(words) > 1000 {
		words = words[:1000]
	}
	for _, w := range words {
		candidates[w.w] = struct{}{}
		candidates[w.w+" "] = struct{}{}
	}

	blobParts := make([]string, 0, len(files))
	rawTokens := 0
	lastTokenizeProgress := time.Now()
	for i, b := range files {
		s := string(b)
		blobParts = append(blobParts, s)
		rawTokens += len(tk.Encode(s, nil, nil))
		if (i+1)%10 == 0 || time.Since(lastTokenizeProgress) >= 2*time.Second {
			reportProgress(opts, Progress{
				Phase:        "analyze",
				Message:      "Tokenizing source files",
				FilesScanned: i + 1,
				BytesScanned: totalBytes,
				Current:      i + 1,
				Total:        len(files),
			})
			lastTokenizeProgress = time.Now()
		}
	}
	blob := strings.Join(blobParts, "\n")

	reportProgress(opts, Progress{
		Phase:        "score",
		Message:      "Scoring candidates for token reduction",
		FilesScanned: len(files),
		BytesScanned: totalBytes,
	})
	escape, codes, err := selectSymbols(opts.Format, opts.DictSize, tk, blob)
	if err != nil {
		return Result{}, err
	}
	replTokenCost := len(tk.Encode(escape+codes[0], nil, nil))
	if opts.Format == codec.VersionTCE2 {
		replTokenCost = len(tk.Encode(codes[0], nil, nil))
	}

	type scored struct {
		text string
		gain int
	}
	scores := make([]scored, 0, len(candidates))
	totalCandidates := len(candidates)
	processedCandidates := 0
	lastScoreProgress := time.Now()
	for cand := range candidates {
		processedCandidates++
		if len(cand) < 2 || strings.Contains(cand, escape) {
			continue
		}
		occ := strings.Count(blob, cand)
		if occ < 2 {
			continue
		}
		from := len(tk.Encode(cand, nil, nil))
		gain := occ * (from - replTokenCost)
		if gain > 0 {
			scores = append(scores, scored{text: cand, gain: gain})
		}
		if processedCandidates%100 == 0 || time.Since(lastScoreProgress) >= 2*time.Second {
			reportProgress(opts, Progress{
				Phase:        "score",
				Message:      "Evaluating dictionary candidates",
				FilesScanned: len(files),
				BytesScanned: totalBytes,
				Current:      processedCandidates,
				Total:        totalCandidates,
			})
			lastScoreProgress = time.Now()
		}
	}
	sort.Slice(scores, func(i, j int) bool {
		if scores[i].gain == scores[j].gain {
			if len(scores[i].text) == len(scores[j].text) {
				return scores[i].text < scores[j].text
			}
			return len(scores[i].text) > len(scores[j].text)
		}
		return scores[i].gain > scores[j].gain
	})

	selected := make([]string, 0, opts.DictSize)
	seen := map[string]struct{}{}
	for _, s := range scores {
		if len(selected) >= opts.DictSize {
			break
		}
		if _, ok := seen[s.text]; ok {
			continue
		}
		seen[s.text] = struct{}{}
		selected = append(selected, s.text)
	}

	cfg := codec.BuildConfig(selected, opts.Format, escape, codes, opts.DictSize)
	cdc, err := codec.NewFromConfig(cfg)
	if err != nil {
		return Result{}, err
	}

	reportProgress(opts, Progress{
		Phase:        "evaluate",
		Message:      "Measuring encoded token savings",
		FilesScanned: len(files),
		BytesScanned: totalBytes,
	})
	encodedTokens := 0
	lastEvalProgress := time.Now()
	for i, b := range files {
		enc := cdc.Encode(b)
		encodedTokens += len(tk.Encode(string(enc), nil, nil))
		if (i+1)%100 == 0 || time.Since(lastEvalProgress) >= 2*time.Second {
			reportProgress(opts, Progress{
				Phase:        "evaluate",
				Message:      "Evaluating compression impact",
				FilesScanned: i + 1,
				BytesScanned: totalBytes,
			})
			lastEvalProgress = time.Now()
		}
	}

	reduction := 0.0
	if rawTokens > 0 {
		reduction = (float64(rawTokens-encodedTokens) / float64(rawTokens)) * 100.0
	}

	usedCodes := make([]string, 0, len(cfg.Entries))
	for _, e := range cfg.Entries {
		usedCodes = append(usedCodes, e.Code)
	}

	result := Result{
		Root:           opts.Root,
		FilesScanned:   len(files),
		BytesScanned:   totalBytes,
		RawTokens:      rawTokens,
		EncodedTokens:  encodedTokens,
		TokenReduction: reduction,
		Entries:        selected,
		Version:        opts.Format,
		Escape:         escape,
		Codes:          usedCodes,
	}
	reportProgress(opts, Progress{
		Phase:        "done",
		Message:      "Dictionary build complete",
		FilesScanned: result.FilesScanned,
		BytesScanned: result.BytesScanned,
	})
	return result, nil
}

func shouldSkipDir(name string, skipDirs []string) bool {
	for _, s := range skipDirs {
		if name == s {
			return true
		}
	}
	return false
}

func selectSymbols(format string, dictSize int, tk *tiktoken.Tiktoken, corpus string) (string, []string, error) {
	if format == codec.VersionTCE1 {
		if dictSize > len(codec.TCE1CodeAlphabet) {
			dictSize = len(codec.TCE1CodeAlphabet)
		}
		codes := make([]string, 0, dictSize)
		for i := 0; i < dictSize; i++ {
			codes = append(codes, codec.TCE1CodeAlphabet[i])
		}
		return "~", codes, nil
	}
	if format != codec.VersionTCE2 {
		return "", nil, fmt.Errorf("unsupported format %q", format)
	}
	need := dictSize + 1
	oneToken := selectOneTokenSymbols(tk, corpus, need)
	if len(oneToken) >= need {
		return oneToken[0], oneToken[1 : dictSize+1], nil
	}
	type cand struct {
		s    string
		cost int
	}
	candidates := make([]cand, 0, 2048)
	for r := rune(0x00A1); r <= rune(0x2BFF); r++ {
		if !utf8.ValidRune(r) || unicode.IsControl(r) || unicode.IsSpace(r) {
			continue
		}
		s := string(r)
		if strings.Contains(corpus, s) {
			continue
		}
		cost := len(tk.Encode(s, nil, nil))
		if cost <= 0 || cost > 3 {
			continue
		}
		candidates = append(candidates, cand{s: s, cost: cost})
	}
	if len(candidates) < need {
		return "", nil, fmt.Errorf("tce2 needs %d symbols, found %d with <=3 token cost", need, len(candidates))
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].cost == candidates[j].cost {
			return candidates[i].s < candidates[j].s
		}
		return candidates[i].cost < candidates[j].cost
	})
	chosen := candidates[:need]
	escape := chosen[0].s
	codes := make([]string, 0, dictSize)
	for i := 1; i < len(chosen); i++ {
		codes = append(codes, chosen[i].s)
	}
	return escape, codes, nil
}

func selectOneTokenSymbols(tk *tiktoken.Tiktoken, corpus string, need int) []string {
	const maxTokenID = 300000
	seen := map[string]struct{}{}
	out := make([]string, 0, need)
	for id := 0; id < maxTokenID && len(out) < need; id++ {
		s := tk.Decode([]int{id})
		if s == "" || !utf8.ValidString(s) || strings.Contains(corpus, s) {
			continue
		}
		if len(tk.Encode(s, nil, nil)) != 1 {
			continue
		}
		r := []rune(s)
		if len(r) != 1 {
			continue
		}
		ch := r[0]
		if unicode.IsControl(ch) || unicode.IsSpace(ch) || unicode.IsLetter(ch) || unicode.IsDigit(ch) {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func reportProgress(opts Options, p Progress) {
	if opts.Progress == nil {
		return
	}
	opts.Progress(p)
}
