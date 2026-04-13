//go:build !tagmem_onnx || !linux

package vector

import (
	"context"
	"fmt"
)

type miniLMEmbedder struct{}

func localBERTSupported() bool { return false }

func loadLocalBERTEmbedder(modelDir string, spec localModelSpec, accel string, state *embeddedRuntimeState) (*miniLMEmbedder, error) {
	if state != nil {
		state.executionDevice = "unsupported"
		state.runtimeLibrary = ""
	}
	return nil, fmt.Errorf("embedded ONNX models are currently supported in linux containers only; use Docker or an OpenAI-compatible backend on this platform")
}

func (e *miniLMEmbedder) Embed(text string) ([]float32, error) {
	return nil, fmt.Errorf("embedded ONNX models are currently supported in linux containers only")
}

func (e *miniLMEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	return nil, fmt.Errorf("embedded ONNX models are currently supported in linux containers only")
}
