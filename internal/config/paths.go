package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	defaultStateDir = ".firebox/state"
	defaultCacheDir = ".firebox/cache"
	defaultLogsDir  = ".firebox/logs"
	daemonRootDir   = ".firebox/daemons"
	defaultSockFile = "fireboxd.sock"
	defaultStateDB  = "state.json"
	defaultRuntime  = "runtime.json"
	defaultImagesDB = "images.json"
)

const DefaultInstanceName = "firebox-host"
const DaemonIDEnvVar = "FIREBOX_DAEMON_ID"

var daemonIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type Paths struct {
	Home     string
	DaemonID string
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
	return ResolvePathsForDaemonID(os.Getenv(DaemonIDEnvVar))
}

func NormalizeDaemonID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", nil
	}
	if !daemonIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid daemon id %q: use 1-64 chars [A-Za-z0-9._-], starting with alphanumeric", raw)
	}
	return id, nil
}

func ResolvePathsForDaemonID(rawDaemonID string) (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve home: %w", err)
	}

	daemonID, err := NormalizeDaemonID(rawDaemonID)
	if err != nil {
		return Paths{}, err
	}

	stateDir := filepath.Join(home, defaultStateDir)
	cacheDir := filepath.Join(home, defaultCacheDir)
	logsDir := filepath.Join(home, defaultLogsDir)
	if daemonID != "" {
		daemonBase := filepath.Join(home, daemonRootDir, daemonID)
		stateDir = filepath.Join(daemonBase, "state")
		cacheDir = filepath.Join(daemonBase, "cache")
		logsDir = filepath.Join(daemonBase, "logs")
	}

	return Paths{
		Home:     home,
		DaemonID: daemonID,
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
