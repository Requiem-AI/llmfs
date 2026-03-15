package setup

import (
	"strings"

	"llmfs/internal/appcfg"
)

const (
	DefaultConfigPath = ".llmfs/config.json"
	DefaultInitPath   = ".llmfs/INIT_INSTRUCTIONS.md"
	DefaultSettings   = ".llmfs/settings.json"
	DefaultMountpoint = ".llmfs/mount"
)

func NormalizeMountpoint(mountpoint string) string {
	mountpoint = strings.TrimSpace(mountpoint)
	if mountpoint == "" {
		return DefaultMountpoint
	}
	return mountpoint
}

func LoadSettings(root, settingsPath string) (appcfg.Settings, error) {
	return appcfg.LoadOrInit(root, settingsPath)
}
