package appcfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

type persistedSettings struct {
	ApplyToAllFiles  bool     `json:"apply_to_all_files"`
	SkipPaths        []string `json:"skip_paths"`
	AvailablePlugins []string `json:"available_plugins"`
	CandidatesFile   string   `json:"candidates_file"`
}

func TestDefaultSettingsMatchExample(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve test file path")
	}
	examplePath := filepath.Join(filepath.Dir(thisFile), "..", "..", "examples", "settings.example.json")

	b, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("read %s: %v", examplePath, err)
	}

	var example persistedSettings
	if err := json.Unmarshal(b, &example); err != nil {
		t.Fatalf("unmarshal %s: %v", examplePath, err)
	}

	def := DefaultSettings()
	got := persistedSettings{
		ApplyToAllFiles:  def.ApplyToAllFiles,
		SkipPaths:        def.SkipPaths,
		AvailablePlugins: def.AvailablePlugins,
		CandidatesFile:   def.CandidatesFile,
	}
	if !reflect.DeepEqual(got, example) {
		t.Fatalf("default settings mismatch\nwant: %+v\ngot:  %+v", example, got)
	}
}
