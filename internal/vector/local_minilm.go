//go:build linux && amd64 && tagmem_onnx

package vector

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

const (
	ortVersion           = "1.24.1"
	ortLinuxAMD64CPUURL  = "https://github.com/microsoft/onnxruntime/releases/download/v1.24.1/onnxruntime-linux-x64-1.24.1.tgz"
	ortLinuxAMD64GPUURL  = "https://github.com/microsoft/onnxruntime/releases/download/v1.24.1/onnxruntime-linux-x64-gpu_cuda13-1.24.1.tgz"
	ortLinuxAMD64URL     = "https://github.com/microsoft/onnxruntime/releases/download/v1.24.1/onnxruntime-linux-x64-1.24.1.tgz"
	ortLibraryLinuxAMD64 = "libonnxruntime.so.1.24.1"
	miniLMMicroBatchSize = 32
)

type miniLMEmbedder struct {
	modelDir  string
	tokenizer *bertTokenizer
	sessions  chan *ort.DynamicAdvancedSession
}

var ortInitOnce sync.Once
var ortInitErr error

func localBERTSupported() bool { return true }

func loadLocalBERTEmbedder(modelDir string, spec localModelSpec, accel string, state *embeddedRuntimeState) (*miniLMEmbedder, error) {
	if _, err := loadBERTTokenizer(filepath.Join(modelDir, "vocab.txt")); err != nil {
		if err := ensureLocalModelAssets(modelDir, spec, false); err != nil {
			return nil, err
		}
		if _, err := loadBERTTokenizer(filepath.Join(modelDir, "vocab.txt")); err != nil {
			return nil, err
		}
	}
	wantGPU := strings.EqualFold(accel, "cuda") || strings.EqualFold(accel, "gpu") || strings.EqualFold(accel, "auto") || accel == ""
	if wantGPU {
		embedder, gpuErr := loadLocalBERTEmbedderWithRuntime(modelDir, spec, true, state)
		if gpuErr == nil {
			return embedder, nil
		}
		if strings.EqualFold(accel, "cuda") || strings.EqualFold(accel, "gpu") {
			return nil, gpuErr
		}
	}
	return loadLocalBERTEmbedderWithRuntime(modelDir, spec, false, state)
}

func loadLocalBERTEmbedderWithRuntime(modelDir string, spec localModelSpec, useGPU bool, state *embeddedRuntimeState) (*miniLMEmbedder, error) {
	if err := ensureLocalModelAssets(modelDir, spec, useGPU); err != nil {
		return nil, err
	}
	vocab, err := loadBERTTokenizer(filepath.Join(modelDir, "vocab.txt"))
	if err != nil {
		return nil, err
	}
	runtimePath := filepath.Join(modelDir, runtimeSubdir(useGPU), ortLibraryName())
	if err := initializeORT(runtimePath); err != nil {
		return nil, err
	}
	options, err := ort.NewSessionOptions()
	if err != nil {
		return nil, err
	}
	defer options.Destroy()
	_ = options.SetGraphOptimizationLevel(ort.GraphOptimizationLevelEnableAll)
	_ = options.SetInterOpNumThreads(1)
	threads := runtime.NumCPU()
	if threads > 8 {
		threads = 8
	}
	if threads < 1 {
		threads = 1
	}
	_ = options.SetIntraOpNumThreads(threads)
	if useGPU {
		cudaOptions, err := ort.NewCUDAProviderOptions()
		if err != nil {
			return nil, err
		}
		defer cudaOptions.Destroy()
		_ = cudaOptions.Update(map[string]string{"device_id": "0"})
		if err := options.AppendExecutionProviderCUDA(cudaOptions); err != nil {
			return nil, err
		}
	}
	poolSize := 1
	if !useGPU && runtime.NumCPU() >= 12 {
		poolSize = 2
	}
	sessions := make(chan *ort.DynamicAdvancedSession, poolSize)
	for i := 0; i < poolSize; i++ {
		session, err := ort.NewDynamicAdvancedSession(filepath.Join(modelDir, "model.onnx"), []string{"input_ids", "attention_mask", "token_type_ids"}, []string{"last_hidden_state"}, options)
		if err != nil {
			return nil, err
		}
		sessions <- session
	}
	if state != nil {
		if useGPU {
			state.executionDevice = "cuda"
		} else {
			state.executionDevice = "cpu"
		}
		state.runtimeLibrary = runtimePath
	}
	return &miniLMEmbedder{modelDir: modelDir, tokenizer: vocab, sessions: sessions}, nil
}

func (e *miniLMEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	type indexedText struct {
		index int
		text  string
	}
	indexed := make([]indexedText, 0, len(texts))
	for i, text := range texts {
		indexed = append(indexed, indexedText{index: i, text: text})
	}
	sort.Slice(indexed, func(i, j int) bool {
		return len(indexed[i].text) < len(indexed[j].text)
	})
	out := make([][]float32, len(texts))
	for start := 0; start < len(indexed); start += miniLMMicroBatchSize {
		end := start + miniLMMicroBatchSize
		if end > len(indexed) {
			end = len(indexed)
		}
		batchTexts := make([]string, 0, end-start)
		for _, item := range indexed[start:end] {
			batchTexts = append(batchTexts, item.text)
		}
		vectors, err := e.embedBatchInternal(batchTexts)
		if err != nil {
			return nil, err
		}
		for i, item := range indexed[start:end] {
			out[item.index] = vectors[i]
		}
	}
	return out, nil
}

func (e *miniLMEmbedder) embedBatchInternal(texts []string) ([][]float32, error) {
	encodedIDs := make([][]int64, 0, len(texts))
	encodedMasks := make([][]int64, 0, len(texts))
	encodedTypes := make([][]int64, 0, len(texts))
	maxLen := 0
	for _, text := range texts {
		ids, mask, typeIDs := e.tokenizer.Encode(text)
		encodedIDs = append(encodedIDs, ids)
		encodedMasks = append(encodedMasks, mask)
		encodedTypes = append(encodedTypes, typeIDs)
		if len(ids) > maxLen {
			maxLen = len(ids)
		}
	}
	batch := len(texts)
	idsFlat := make([]int64, batch*maxLen)
	maskFlat := make([]int64, batch*maxLen)
	typeFlat := make([]int64, batch*maxLen)
	for i := 0; i < batch; i++ {
		for j := 0; j < maxLen; j++ {
			base := i*maxLen + j
			idsFlat[base] = int64(e.tokenizer.padID)
			if j < len(encodedIDs[i]) {
				idsFlat[base] = encodedIDs[i][j]
				maskFlat[base] = encodedMasks[i][j]
				typeFlat[base] = encodedTypes[i][j]
			}
		}
	}
	shape := ort.NewShape(int64(batch), int64(maxLen))
	inputIDs, err := ort.NewTensor(shape, idsFlat)
	if err != nil {
		return nil, err
	}
	defer inputIDs.Destroy()
	attentionMask, err := ort.NewTensor(shape, maskFlat)
	if err != nil {
		return nil, err
	}
	defer attentionMask.Destroy()
	tokenTypeIDs, err := ort.NewTensor(shape, typeFlat)
	if err != nil {
		return nil, err
	}
	defer tokenTypeIDs.Destroy()
	outputs := []ort.Value{nil}
	session := <-e.sessions
	err = session.Run([]ort.Value{inputIDs, attentionMask, tokenTypeIDs}, outputs)
	e.sessions <- session
	if err != nil {
		return nil, err
	}
	outputTensor, ok := outputs[0].(*ort.Tensor[float32])
	if !ok || outputTensor == nil {
		return nil, fmt.Errorf("unexpected model output type")
	}
	defer outputTensor.Destroy()
	return meanPoolNormalizeBatch(outputTensor.GetData(), outputTensor.GetShape(), encodedMasks)
}

func (e *miniLMEmbedder) Embed(text string) ([]float32, error) {
	vectors, err := e.embedBatchInternal([]string{text})
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("unexpected batch size %d", len(vectors))
	}
	return vectors[0], nil
}

func meanPoolNormalize(data []float32, shape ort.Shape, mask []int64) ([]float32, error) {
	if len(shape) != 3 || shape[0] != 1 {
		return nil, fmt.Errorf("unexpected output shape %v", shape)
	}
	sequenceLength := int(shape[1])
	hiddenSize := int(shape[2])
	if sequenceLength == 0 || hiddenSize == 0 {
		return nil, fmt.Errorf("invalid output shape %v", shape)
	}
	vector := make([]float32, hiddenSize)
	var count float32
	for token := 0; token < sequenceLength && token < len(mask); token++ {
		if mask[token] == 0 {
			continue
		}
		base := token * hiddenSize
		for i := 0; i < hiddenSize; i++ {
			vector[i] += data[base+i]
		}
		count++
	}
	if count == 0 {
		return nil, errors.New("no attended tokens")
	}
	for i := range vector {
		vector[i] /= count
	}
	normalize(vector)
	return vector, nil
}

func meanPoolNormalizeBatch(data []float32, shape ort.Shape, masks [][]int64) ([][]float32, error) {
	if len(shape) != 3 {
		return nil, fmt.Errorf("unexpected output shape %v", shape)
	}
	batch := int(shape[0])
	sequenceLength := int(shape[1])
	hiddenSize := int(shape[2])
	out := make([][]float32, 0, batch)
	for row := 0; row < batch; row++ {
		vector := make([]float32, hiddenSize)
		var count float32
		mask := masks[row]
		for token := 0; token < sequenceLength && token < len(mask); token++ {
			if mask[token] == 0 {
				continue
			}
			base := row*sequenceLength*hiddenSize + token*hiddenSize
			for i := 0; i < hiddenSize; i++ {
				vector[i] += data[base+i]
			}
			count++
		}
		if count == 0 {
			return nil, errors.New("no attended tokens")
		}
		for i := range vector {
			vector[i] /= count
		}
		normalize(vector)
		out = append(out, vector)
	}
	return out, nil
}

func ensureLocalModelAssets(modelDir string, spec localModelSpec, useGPU bool) error {
	if err := os.MkdirAll(filepath.Join(modelDir, runtimeSubdir(useGPU)), 0o755); err != nil {
		return err
	}
	baseURL := "https://huggingface.co/" + spec.HFRepo + "/resolve/main"
	assets := []struct{ path, url string }{
		{filepath.Join(modelDir, "model.onnx"), baseURL + "/onnx/model.onnx"},
		{filepath.Join(modelDir, "vocab.txt"), baseURL + "/vocab.txt"},
	}
	for _, asset := range assets {
		if _, err := os.Stat(asset.path); err == nil {
			continue
		}
		if err := downloadFile(asset.path, asset.url); err != nil {
			return err
		}
	}
	libPath := filepath.Join(modelDir, runtimeSubdir(useGPU), ortLibraryName())
	if _, err := os.Stat(libPath); err == nil {
		return nil
	}
	return downloadAndExtractORT(filepath.Join(modelDir, runtimeSubdir(useGPU)), useGPU)
}

func initializeORT(libraryPath string) error {
	ortInitOnce.Do(func() {
		ort.SetSharedLibraryPath(libraryPath)
		ortInitErr = ort.InitializeEnvironment()
	})
	return ortInitErr
}

func ortLibraryName() string {
	if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		return ortLibraryLinuxAMD64
	}
	return ""
}

func downloadAndExtractORT(runtimeDir string, useGPU bool) error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return fmt.Errorf("embedded ONNX runtime currently supports linux/amd64 only")
	}
	url := ortLinuxAMD64CPUURL
	if useGPU {
		url = ortLinuxAMD64GPUURL
	}
	response, err := http.Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download onnxruntime: %s", response.Status)
	}
	gz, err := gzip.NewReader(response.Body)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	libFiles := map[string]struct{}{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if !strings.Contains(header.Name, "/lib/") || !strings.HasPrefix(filepath.Base(header.Name), "libonnxruntime") {
			continue
		}
		name := filepath.Base(header.Name)
		outPath := filepath.Join(runtimeDir, name)
		switch header.Typeflag {
		case tar.TypeSymlink:
			_ = os.Remove(outPath)
			if err := os.Symlink(filepath.Base(header.Linkname), outPath); err != nil {
				return err
			}
		default:
			file, err := os.Create(outPath)
			if err != nil {
				return err
			}
			if _, err := io.Copy(file, tr); err != nil {
				file.Close()
				return err
			}
			if err := file.Close(); err != nil {
				return err
			}
		}
		libFiles[name] = struct{}{}
	}
	if _, ok := libFiles[ortLibraryLinuxAMD64]; !ok {
		return fmt.Errorf("onnxruntime library not found in archive")
	}
	return nil
}

func runtimeSubdir(useGPU bool) string {
	if useGPU {
		return "runtime-cuda"
	}
	return "runtime-cpu"
}

func downloadFile(path, url string) error {
	response, err := http.Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download %s: %s", url, response.Status)
	}
	tmp := path + ".tmp"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(file, response.Body); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
