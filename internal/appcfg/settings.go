package appcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Settings struct {
	ApplyToAllFiles bool         `json:"apply_to_all_files"`
	SkipDirs        []string     `json:"skip_dirs"`
	Candidates      []string     `json:"candidates"`
	Middlewares     []Middleware `json:"middlewares"`
	SkipDirsFile    string       `json:"skip_dirs_file"`
	CandidatesFile  string       `json:"candidates_file"`
}

type Middleware struct {
	Name    string         `json:"name"`
	Enabled bool           `json:"enabled"`
	Options map[string]any `json:"options,omitempty"`
}

type partialSettings struct {
	ApplyToAllFiles *bool         `json:"apply_to_all_files"`
	SkipDirs        []string      `json:"skip_dirs"`
	Candidates      []string      `json:"candidates"`
	Middlewares     *[]Middleware `json:"middlewares"`
	SkipDirsFile    string        `json:"skip_dirs_file"`
	CandidatesFile  string        `json:"candidates_file"`
}

func DefaultSettings() Settings {
	return Settings{
		ApplyToAllFiles: true,
		SkipDirs: []string{
			".git", ".llmfs", "node_modules", "vendor", "dist", "build", "bin", "out", "coverage",
			".idea", ".vscode", ".venv", "venv", "target", ".next", ".turbo",
		},
		Candidates: defaultCandidates(),
		Middlewares: []Middleware{
			{
				Name:    "deny_env_dotfiles",
				Enabled: true,
			},
			{
				Name:    "redirect_env_to_agent",
				Enabled: true,
			},
			{
				Name:    "codec",
				Enabled: true,
			},
		},
		SkipDirsFile:   ".llmfs/skip_dirs.txt",
		CandidatesFile: ".llmfs/candidates.txt",
	}
}

func LoadOrInit(root, settingsPath string) (Settings, error) {
	cfg := DefaultSettings()
	if settingsPath == "" {
		settingsPath = ".llmfs/settings.json"
	}
	settingsAbs := resolve(root, settingsPath)
	if err := os.MkdirAll(filepath.Dir(settingsAbs), 0o755); err != nil {
		return Settings{}, err
	}

	if _, err := os.Stat(settingsAbs); os.IsNotExist(err) {
		if err := writeDefaultFiles(root, cfg); err != nil {
			return Settings{}, err
		}
		if err := writeJSON(settingsAbs, cfg); err != nil {
			return Settings{}, err
		}
	} else {
		b, err := os.ReadFile(settingsAbs)
		if err != nil {
			return Settings{}, err
		}
		var p partialSettings
		if err := json.Unmarshal(b, &p); err != nil {
			return Settings{}, err
		}
		if p.ApplyToAllFiles != nil {
			cfg.ApplyToAllFiles = *p.ApplyToAllFiles
		}
		if len(p.SkipDirs) > 0 {
			cfg.SkipDirs = append([]string(nil), p.SkipDirs...)
		}
		if len(p.Candidates) > 0 {
			cfg.Candidates = append([]string(nil), p.Candidates...)
		}
		if p.Middlewares != nil {
			cfg.Middlewares = normalizeMiddlewares(*p.Middlewares)
		}
		if p.SkipDirsFile != "" {
			cfg.SkipDirsFile = p.SkipDirsFile
		}
		if p.CandidatesFile != "" {
			cfg.CandidatesFile = p.CandidatesFile
		}
	}

	skipFile := resolve(root, cfg.SkipDirsFile)
	candFile := resolve(root, cfg.CandidatesFile)
	if _, err := os.Stat(skipFile); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(skipFile), 0o755); err != nil {
			return Settings{}, err
		}
		if err := os.WriteFile(skipFile, []byte(strings.Join(cfg.SkipDirs, "\n")+"\n"), 0o644); err != nil {
			return Settings{}, err
		}
	}
	if _, err := os.Stat(candFile); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(candFile), 0o755); err != nil {
			return Settings{}, err
		}
		if err := os.WriteFile(candFile, []byte(strings.Join(cfg.Candidates, "\n")+"\n"), 0o644); err != nil {
			return Settings{}, err
		}
	}

	if lines, err := readLines(skipFile); err == nil && len(lines) > 0 {
		cfg.SkipDirs = lines
	}
	if lines, err := readLines(candFile); err == nil && len(lines) > 0 {
		cfg.Candidates = lines
	}

	cfg.SkipDirs = normalizeSet(cfg.SkipDirs)
	cfg.Candidates = uniqueKeepOrder(cfg.Candidates)
	cfg.Middlewares = normalizeMiddlewares(cfg.Middlewares)
	return cfg, nil
}

func writeDefaultFiles(root string, cfg Settings) error {
	skipFile := resolve(root, cfg.SkipDirsFile)
	candFile := resolve(root, cfg.CandidatesFile)
	if err := os.MkdirAll(filepath.Dir(skipFile), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(candFile), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(skipFile, []byte(strings.Join(cfg.SkipDirs, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(candFile, []byte(strings.Join(cfg.Candidates, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}

func resolve(root, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

func readLines(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	rows := strings.Split(string(b), "\n")
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		r = strings.TrimSpace(r)
		if r == "" || strings.HasPrefix(r, "#") {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func normalizeSet(in []string) []string {
	m := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := m[v]; ok {
			continue
		}
		m[v] = struct{}{}
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func uniqueKeepOrder(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if strings.TrimSpace(v) == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func normalizeMiddlewares(in []Middleware) []Middleware {
	if len(in) == 0 {
		return []Middleware{}
	}
	out := make([]Middleware, 0, len(in))
	for _, m := range in {
		name := strings.TrimSpace(m.Name)
		if name == "" {
			continue
		}
		normalized := Middleware{
			Name:    name,
			Enabled: m.Enabled,
		}
		if m.Options != nil {
			normalized.Options = m.Options
		}
		out = append(out, normalized)
	}
	return out
}

func defaultCandidates() []string {
	return []string{
		"package ", "import ", "func ", "return ", "if ", " else ", "for ", "range ", "switch ", "case ",
		"class ", "interface ", "struct ", "type ", "enum ", "namespace ", "module ", "trait ", "impl ",
		"var ", "let ", "const ", "final ", "public ", "private ", "protected ", "static ", "async ", "await ",
		"try ", "catch ", "finally ", "throw ", "throws ", "except", "def ", "lambda", "fn ", "mut ",
		" => ", " -> ", " := ", " == ", " != ", " <= ", " >= ", " && ", " || ", "===", "!==", "::", "=>",
		"()", "{}", "[]", "<", ">", "/>", "\n\t", "\n    ", "\n\n", "\n- ", "\n* ", "\n# ", "\n## ", "\n### ",
		"SELECT ", "FROM ", "WHERE ", "GROUP BY ", "ORDER BY ", "INSERT INTO ", "UPDATE ", "DELETE FROM ",
		"BEGIN", "END", "function ", "procedure ", "pragma ",
		"{\n", "\n}", "\"\"\"", "'''", "```", "<!--", "-->", "/*", "*/", "// ",
		"http://", "https://", "github.com/", "localhost", "127.0.0.1",
		"error", "context", "string", "int", "bool", "nil", "null", "true", "false", "undefined",
		"TODO", "FIXME", "NOTE", "HACK", "BUG", "README", "LICENSE",
	}
}
