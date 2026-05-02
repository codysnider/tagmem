package vector

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codysnider/tagmem/internal/xdg"
)

func testPaths() xdg.Paths {
	return xdg.Paths{ModelDir: "/tmp/tiered-memory-test-models"}
}

func TestProviderFromEnvDefaultsToEmbedded(t *testing.T) {
	t.Setenv("TAGMEM_EMBED_PROVIDER", "")

	provider, err := ProviderFromEnv(testPaths())
	if err != nil {
		t.Fatalf("ProviderFromEnv() error = %v", err)
	}
	if provider.Name != ProviderEmbedded {
		t.Fatalf("provider.Name = %q, want %q", provider.Name, ProviderEmbedded)
	}
}

func TestProviderFromEnvReadsOllamaConfig(t *testing.T) {
	t.Setenv("TAGMEM_EMBED_PROVIDER", "openai")
	t.Setenv("TAGMEM_OPENAI_MODEL", "bge-m3")
	t.Setenv("TAGMEM_OPENAI_BASE_URL", "http://localhost:11434/v1")
	t.Setenv("TAGMEM_OPENAI_API_KEY", "secret")

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
	t.Setenv("TAGMEM_EMBED_PROVIDER", "mystery")

	_, err := ProviderFromEnv(testPaths())
	if err == nil {
		t.Fatal("ProviderFromEnv() error = nil, want non-nil")
	}
}

func TestProviderFromEnvRejectsRemovedHashProviders(t *testing.T) {
	for _, providerName := range []string{"hash", "embedded-hash"} {
		t.Run(providerName, func(t *testing.T) {
			t.Setenv("TAGMEM_EMBED_PROVIDER", providerName)

			_, err := ProviderFromEnv(testPaths())
			if err == nil {
				t.Fatal("ProviderFromEnv() error = nil, want non-nil")
			}
			if !strings.Contains(strings.ToLower(err.Error()), "unsupported") {
				t.Fatalf("ProviderFromEnv() error = %q, want unsupported provider error", err)
			}
		})
	}
}

func TestProviderFromEnvFallsBackToOllamaHost(t *testing.T) {
	t.Setenv("TAGMEM_EMBED_PROVIDER", "openai")
	t.Setenv("TAGMEM_OPENAI_BASE_URL", "")
	t.Setenv("OLLAMA_HOST", "http://localhost:11434")

	provider, err := ProviderFromEnv(testPaths())
	if err != nil {
		t.Fatalf("ProviderFromEnv() error = %v", err)
	}
	if provider.BaseURL != "http://localhost:11434/v1" {
		t.Fatalf("provider.BaseURL = %q, want %q", provider.BaseURL, "http://localhost:11434/v1")
	}
}

func TestEmbeddedProviderFailsWhenEmbeddedRuntimeUnsupported(t *testing.T) {
	originalLoadLocalBERTEmbedder := loadLocalBERTEmbedderFunc
	loadLocalBERTEmbedderFunc = func(modelDir string, spec localModelSpec, accel string, state *embeddedRuntimeState) (*miniLMEmbedder, error) {
		if state != nil {
			state.executionDevice = "unsupported"
		}
		return nil, errors.New("embedded runtime unsupported in test")
	}
	t.Cleanup(func() {
		loadLocalBERTEmbedderFunc = originalLoadLocalBERTEmbedder
	})

	provider, err := EmbeddedProvider(testPaths(), defaultEmbeddedModel, "auto")
	if err != nil {
		t.Fatalf("EmbeddedProvider() error = %v, want nil", err)
	}
	if provider.Name != ProviderEmbedded {
		t.Fatalf("provider.Name = %q, want %q", provider.Name, ProviderEmbedded)
	}

	_, err = provider.Func(t.Context(), "test text")
	if err == nil {
		t.Fatal("provider.Func() error = nil, want non-nil")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "unsupported") {
		t.Fatalf("provider.Func() error = %q, want unsupported-runtime error", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "hash") {
		t.Fatalf("provider.Func() error = %q, want no hash fallback reference", err)
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

func TestEmbeddingDocsAndScriptsDoNotReferenceRemovedHashFallback(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))

	readme, err := os.ReadFile(filepath.Join(repoRoot, "README.md"))
	if err != nil {
		t.Fatalf("ReadFile(README.md) error = %v", err)
	}
	if strings.Contains(string(readme), "embedded-hash") {
		t.Fatal("README.md still documents embedded-hash as a supported provider")
	}

	for _, relativePath := range []string{
		"scripts/install.sh",
		"scripts/cmd/release-image/run.sh",
		"scripts/cmd/release-image-arm64-remote/run.sh",
	} {
		data, err := os.ReadFile(filepath.Join(repoRoot, relativePath))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", relativePath, err)
		}
		if strings.Contains(string(data), "embedded hash fallback") {
			t.Fatalf("%s still contains removed embedded hash fallback string logic", relativePath)
		}
	}
}
