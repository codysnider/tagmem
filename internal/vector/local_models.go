package vector

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/codysnider/tagmem/internal/xdg"
)

type localModelSpec struct {
	Name        string
	HFRepo      string
	Description string
}

type embeddedRuntimeState struct {
	executionDevice string
	runtimeLibrary  string
}

var localModelSpecs = map[string]localModelSpec{
	"all-minilm-l6-v2": {
		Name:        "all-MiniLM-L6-v2",
		HFRepo:      "Xenova/all-MiniLM-L6-v2",
		Description: "embedded local ONNX model all-MiniLM-L6-v2",
	},
	"bge-small-en-v1-5": {
		Name:        "bge-small-en-v1.5",
		HFRepo:      "Xenova/bge-small-en-v1.5",
		Description: "embedded local ONNX model bge-small-en-v1.5",
	},
	"bge-base-en-v1-5": {
		Name:        "bge-base-en-v1.5",
		HFRepo:      "Xenova/bge-base-en-v1.5",
		Description: "embedded local ONNX model bge-base-en-v1.5",
	},
}

func EmbeddedProvider(paths xdg.Paths, modelName, accel string) (Provider, error) {
	key := sanitizeLocalModel(modelName)
	spec, ok := localModelSpecs[key]
	if !ok {
		return Provider{}, fmt.Errorf("unsupported embedded model %q", modelName)
	}
	modelDir := filepath.Join(paths.ModelDir, spec.Name)
	state := &embeddedRuntimeState{executionDevice: "pending"}
	if !localBERTSupported() {
		state.executionDevice = "unsupported"
		provider := EmbeddedHashProvider()
		provider.Description = provider.Description + " (fallback from unsupported local ONNX runtime)"
		provider.Details = func() map[string]string {
			return map[string]string{"execution_device": state.executionDevice, "runtime_library": state.runtimeLibrary}
		}
		return provider, nil
	}
	var (
		once        sync.Once
		embedder    *miniLMEmbedder
		embedderErr error
	)
	return Provider{
		Name:        ProviderEmbedded,
		IndexKey:    ProviderEmbedded + "-" + sanitizeKey(spec.Name),
		Description: spec.Description,
		Model:       spec.Name,
		Func: func(ctx context.Context, text string) ([]float32, error) {
			once.Do(func() {
				embedder, embedderErr = loadLocalBERTEmbedder(modelDir, spec, accel, state)
			})
			if embedderErr != nil {
				return nil, embedderErr
			}
			return embedder.EmbeddingFunc()(ctx, text)
		},
		Batch: func(ctx context.Context, texts []string) ([][]float32, error) {
			once.Do(func() {
				embedder, embedderErr = loadLocalBERTEmbedder(modelDir, spec, accel, state)
			})
			if embedderErr != nil {
				return nil, embedderErr
			}
			return embedder.EmbedBatch(ctx, texts)
		},
		Details: func() map[string]string {
			return map[string]string{"execution_device": state.executionDevice, "runtime_library": state.runtimeLibrary}
		},
	}, nil
}

func sanitizeLocalModel(modelName string) string {
	key := sanitizeKey(modelName)
	if key == "" {
		return "all-minilm-l6-v2"
	}
	return key
}
