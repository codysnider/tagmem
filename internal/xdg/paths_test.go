package xdg

import (
	"path/filepath"
	"testing"
)

func TestResolveUsesTagmemOverrides(t *testing.T) {
	t.Setenv("TAGMEM_DATA_ROOT", "/tmp/tagmem-data")
	t.Setenv("TAGMEM_CONFIG_ROOT", "/tmp/tagmem-config")
	t.Setenv("TAGMEM_CACHE_ROOT", "/tmp/tagmem-cache")

	paths, err := Resolve("tagmem")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if paths.DataDir != "/tmp/tagmem-data" {
		t.Fatalf("DataDir = %q, want %q", paths.DataDir, "/tmp/tagmem-data")
	}
	if paths.ConfigDir != "/tmp/tagmem-config" {
		t.Fatalf("ConfigDir = %q, want %q", paths.ConfigDir, "/tmp/tagmem-config")
	}
	if paths.CacheDir != "/tmp/tagmem-cache" {
		t.Fatalf("CacheDir = %q, want %q", paths.CacheDir, "/tmp/tagmem-cache")
	}
	if paths.StorePath != filepath.Join("/tmp/tagmem-data", "store.json") {
		t.Fatalf("StorePath = %q", paths.StorePath)
	}
	if paths.ConfigPath != filepath.Join("/tmp/tagmem-config", "config.json") {
		t.Fatalf("ConfigPath = %q", paths.ConfigPath)
	}
}

func TestResolveFallsBackToXDGRoots(t *testing.T) {
	t.Setenv("TAGMEM_DATA_ROOT", "")
	t.Setenv("TAGMEM_CONFIG_ROOT", "")
	t.Setenv("TAGMEM_CACHE_ROOT", "")
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-data")
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg-config")
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-cache")

	paths, err := Resolve("tagmem")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if paths.DataDir != filepath.Join("/tmp/xdg-data", "tagmem") {
		t.Fatalf("DataDir = %q", paths.DataDir)
	}
	if paths.ConfigDir != filepath.Join("/tmp/xdg-config", "tagmem") {
		t.Fatalf("ConfigDir = %q", paths.ConfigDir)
	}
	if paths.CacheDir != filepath.Join("/tmp/xdg-cache", "tagmem") {
		t.Fatalf("CacheDir = %q", paths.CacheDir)
	}
}
