package vector

import (
	"strings"
	"testing"

	"github.com/codysnider/tagmem/internal/xdg"
)

func testPaths() xdg.Paths {
	return xdg.Paths{ModelDir: "/tmp/tiered-memory-test-models"}
}

func TestProviderFromEnvDefaultsToEmbedded(t *testing.T) {
	t.Setenv("TIERED_MEMORY_EMBED_PROVIDER", "")
	t.Setenv("TM_EMBED_PROVIDER", "")

	provider, err := ProviderFromEnv(testPaths())
	if err != nil {
		t.Fatalf("ProviderFromEnv() error = %v", err)
	}
	if provider.Name != ProviderEmbedded {
		t.Fatalf("provider.Name = %q, want %q", provider.Name, ProviderEmbedded)
	}
}

func TestProviderFromEnvReadsOllamaConfig(t *testing.T) {
	t.Setenv("TIERED_MEMORY_EMBED_PROVIDER", "openai")
	t.Setenv("TIERED_MEMORY_OPENAI_MODEL", "bge-m3")
	t.Setenv("TIERED_MEMORY_OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("TIERED_MEMORY_OPENAI_API_KEY", "secret")

	provider, err := ProviderFromEnv(testPaths())
	if err != nil {
		t.Fatalf("ProviderFromEnv() error = %v", err)
	}
	if provider.Name != ProviderOpenAI {
		t.Fatalf("provider.Name = %q, want %q", provider.Name, ProviderOpenAI)
	}
	if provider.IndexKey != "openai-bge-m3" {
		t.Fatalf("provider.IndexKey = %q, want %q", provider.IndexKey, "openai-bge-m3")
	}
	if provider.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("provider.BaseURL = %q, want %q", provider.BaseURL, "http://localhost:11434/v1")
	}
}

func TestProviderFromEnvRejectsUnknownProvider(t *testing.T) {
	t.Setenv("TIERED_MEMORY_EMBED_PROVIDER", "mystery")

	_, err := ProviderFromEnv(testPaths())
	if err == nil {
		t.Fatal("ProviderFromEnv() error = nil, want non-nil")
	}
}

func TestProviderFromEnvFallsBackToOllamaHost(t *testing.T) {
	t.Setenv("TIERED_MEMORY_EMBED_PROVIDER", "openai")
	t.Setenv("TIERED_MEMORY_OPENAI_BASE_URL", "")
	t.Setenv("TM_OPENAI_BASE_URL", "")
	t.Setenv("OLLAMA_HOST", "http://10.20.0.2:11434")

	provider, err := ProviderFromEnv(testPaths())
	if err != nil {
		t.Fatalf("ProviderFromEnv() error = %v", err)
	}
	if provider.BaseURL != "http://10.20.0.2:11434/v1" {
		t.Fatalf("provider.BaseURL = %q, want %q", provider.BaseURL, "http://10.20.0.2:11434/v1")
	}
}

func TestDiagnoseDoctorErrorForMissingEmbeddings(t *testing.T) {
	t.Parallel()

	diagnosis, hint := diagnoseDoctorError(Provider{Name: ProviderOpenAI}, "no embeddings found in the response")
	if diagnosis == "" {
		t.Fatal("diagnosis = empty, want value")
	}
	if !strings.Contains(strings.ToLower(hint), "dedicated embeddings model") {
		t.Fatalf("hint = %q, want embeddings guidance", hint)
	}
}
