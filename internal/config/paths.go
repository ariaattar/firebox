package config

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	defaultStateDir = ".firebox/state"
	defaultCacheDir = ".firebox/cache"
	defaultLogsDir  = ".firebox/logs"
	defaultSockFile = "fireboxd.sock"
	defaultStateDB  = "state.json"
	defaultRuntime  = "runtime.json"
	defaultImagesDB = "images.json"
)

const DefaultInstanceName = "firebox-host"

type Paths struct {
	Home     string
	StateDir string
	CacheDir string
	LogsDir  string
	SockPath string
	StateDB  string
	Runtime  string
	ImagesDB string
	LogFile  string
}

func ResolvePaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home: %w", err)
	}

	stateDir := filepath.Join(home, defaultStateDir)
	cacheDir := filepath.Join(home, defaultCacheDir)
	logsDir := filepath.Join(home, defaultLogsDir)

	return Paths{
		Home:     home,
		StateDir: stateDir,
		CacheDir: cacheDir,
		LogsDir:  logsDir,
		SockPath: filepath.Join(stateDir, defaultSockFile),
		StateDB:  filepath.Join(stateDir, defaultStateDB),
		Runtime:  filepath.Join(stateDir, defaultRuntime),
		ImagesDB: filepath.Join(stateDir, defaultImagesDB),
		LogFile:  filepath.Join(logsDir, "fireboxd.log"),
	}, nil
}

func EnsureDirs(p Paths) error {
	for _, d := range []string{p.StateDir, p.CacheDir, p.LogsDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}
