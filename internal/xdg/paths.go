package xdg

import (
	"fmt"
	"os"
	"path/filepath"
)

type Paths struct {
	AppName      string
	ConfigDir    string
	DataDir      string
	CacheDir     string
	IndexDir     string
	ModelDir     string
	DiaryDir     string
	StorePath    string
	ConfigPath   string
	KGPath       string
	IdentityPath string
}

func Resolve(appName string) (Paths, error) {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve config dir: %w", err)
	}

	dataRoot, err := userDataDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve data dir: %w", err)
	}

	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve cache dir: %w", err)
	}

	configDir := filepath.Join(configRoot, appName)
	dataDir := filepath.Join(dataRoot, appName)
	cacheDir := filepath.Join(cacheRoot, appName)

	return Paths{
		AppName:      appName,
		ConfigDir:    configDir,
		DataDir:      dataDir,
		CacheDir:     cacheDir,
		IndexDir:     filepath.Join(dataDir, "vector"),
		ModelDir:     filepath.Join(dataDir, "models"),
		DiaryDir:     filepath.Join(dataDir, "diaries"),
		StorePath:    filepath.Join(dataDir, "store.json"),
		ConfigPath:   filepath.Join(configDir, "config.json"),
		KGPath:       filepath.Join(dataDir, "knowledge.json"),
		IdentityPath: filepath.Join(configDir, "identity.txt"),
	}, nil
}

func (p Paths) Ensure() error {
	for _, dir := range []string{p.ConfigDir, p.DataDir, p.CacheDir, p.IndexDir, p.ModelDir, p.DiaryDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	return nil
}

func userDataDir() (string, error) {
	if dataHome := os.Getenv("XDG_DATA_HOME"); dataHome != "" {
		return dataHome, nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(homeDir, ".local", "share"), nil
}
