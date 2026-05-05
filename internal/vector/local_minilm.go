//go:build linux && tagmem_onnx

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
	ortLinuxARM64CPUURL  = "https://github.com/microsoft/onnxruntime/releases/download/v1.24.1/onnxruntime-linux-aarch64-1.24.1.tgz"
	ortLibraryLinuxAMD64 = "libonnxruntime.so.1.24.1"
	ortLibraryLinuxARM64 = "libonnxruntime.so.1.24.1"
	miniLMMicroBatchSize = 32
)

type ortRuntimeSpec struct {
	url         string
	libraryName string
}

type miniLMEmbedder struct {
	modelDir       string
	tokenizer      *bertTokenizer
	sessions       chan *ort.DynamicAdvancedSession
	embeddingCache *embeddingCache
}

var ortInitOnce sync.Once
var ortInitErr error

func init() {
	if miniLMEmbedBatchInternalFunc == nil {
		miniLMEmbedBatchInternalFunc = func(e *miniLMEmbedder, texts []string, profiler embeddedProfiler) ([][]float32, error) {
			return e.embedBatchInternal(texts, profiler)
		}
	}
	if setMiniLMEmbedderCacheForTest == nil {
		setMiniLMEmbedderCacheForTest = func(embedder *miniLMEmbedder, cache *embeddingCache) bool {
			if embedder == nil {
				return false
			}
			embedder.embeddingCache = cache
			return true
		}
	}
	if getMiniLMEmbedderCacheForTest == nil {
		getMiniLMEmbedderCacheForTest = func(embedder *miniLMEmbedder) (*embeddingCache, bool) {
			if embedder == nil || embedder.embeddingCache == nil {
				return nil, false
			}
			return embedder.embeddingCache, true
		}
	}
}

func localBERTSupported() bool {
	return runtime.GOARCH == "amd64" || runtime.GOARCH == "arm64"
}

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
	runtimeSpec, err := currentORTRuntimeSpec(useGPU)
	if err != nil {
		return nil, err
	}
	runtimePath := filepath.Join(modelDir, runtimeSubdir(useGPU), runtimeSpec.libraryName)
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
	return &miniLMEmbedder{
		modelDir:       modelDir,
		tokenizer:      vocab,
		sessions:       sessions,
		embeddingCache: newEmbeddingCache(1024),
	}, nil
}

func (e *miniLMEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	profiler := newEmbeddedProfiler(nil)
	defer profiler.begin("embed_total")()
	out := make([][]float32, len(texts))
	missIndexesByText := make(map[string][]int)
	misses := make([]string, 0, len(texts))
	for i, text := range texts {
		if vector, ok := e.embeddingCache.get(text); ok {
			out[i] = vector
			continue
		}
		if _, seen := missIndexesByText[text]; !seen {
			misses = append(misses, text)
		}
		missIndexesByText[text] = append(missIndexesByText[text], i)
	}
	if len(misses) == 0 {
		return out, nil
	}
	type indexedText struct {
		index int
		text  string
	}
	indexed := make([]indexedText, 0, len(misses))
	for i, text := range misses {
		indexed = append(indexed, indexedText{index: i, text: text})
	}
	sort.Slice(indexed, func(i, j int) bool {
		return len(indexed[i].text) < len(indexed[j].text)
	})
	computed := make([][]float32, len(misses))
	for start := 0; start < len(indexed); start += miniLMMicroBatchSize {
		end := start + miniLMMicroBatchSize
		if end > len(indexed) {
			end = len(indexed)
		}
		batchTexts := make([]string, 0, end-start)
		for _, item := range indexed[start:end] {
			batchTexts = append(batchTexts, item.text)
		}
		vectors, err := miniLMEmbedBatchInternalFunc(e, batchTexts, profiler)
		if err != nil {
			return nil, err
		}
		for i, item := range indexed[start:end] {
			computed[item.index] = vectors[i]
		}
	}
	for missIndex, text := range misses {
		vector := computed[missIndex]
		e.embeddingCache.put(text, vector)
		for _, outputIndex := range missIndexesByText[text] {
			out[outputIndex] = cloneEmbedding(vector)
		}
	}
	return out, nil
}

type miniLMTokenizedBatch struct {
	encodedIDs   [][]int64
	encodedMasks [][]int64
	encodedTypes [][]int64
	maxLen       int
	batchSize    int
	defaultPadID int64
}

type miniLMTensorBatch struct {
	shape         ort.Shape
	inputIDs      *ort.Tensor[int64]
	attentionMask *ort.Tensor[int64]
	tokenTypeIDs  *ort.Tensor[int64]
}

type miniLMRunOutput struct {
	data  []float32
	shape ort.Shape
}

func (e *miniLMEmbedder) embedBatchInternal(texts []string, profiler embeddedProfiler) ([][]float32, error) {
	return runEmbeddedBatchProfiled(texts, profiler, embeddedBatchProfileOps[miniLMTokenizedBatch, miniLMTensorBatch, *ort.DynamicAdvancedSession, miniLMRunOutput]{
		Tokenize: func(texts []string) (miniLMTokenizedBatch, error) {
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
			return miniLMTokenizedBatch{
				encodedIDs:   encodedIDs,
				encodedMasks: encodedMasks,
				encodedTypes: encodedTypes,
				maxLen:       maxLen,
				batchSize:    len(texts),
				defaultPadID: int64(e.tokenizer.padID),
			}, nil
		},
		TensorPrepare: func(tokenized miniLMTokenizedBatch) (miniLMTensorBatch, error) {
			idsFlat := make([]int64, tokenized.batchSize*tokenized.maxLen)
			maskFlat := make([]int64, tokenized.batchSize*tokenized.maxLen)
			typeFlat := make([]int64, tokenized.batchSize*tokenized.maxLen)
			for i := 0; i < tokenized.batchSize; i++ {
				for j := 0; j < tokenized.maxLen; j++ {
					base := i*tokenized.maxLen + j
					idsFlat[base] = tokenized.defaultPadID
					if j < len(tokenized.encodedIDs[i]) {
						idsFlat[base] = tokenized.encodedIDs[i][j]
						maskFlat[base] = tokenized.encodedMasks[i][j]
						typeFlat[base] = tokenized.encodedTypes[i][j]
					}
				}
			}
			shape := ort.NewShape(int64(tokenized.batchSize), int64(tokenized.maxLen))
			inputIDs, err := ort.NewTensor(shape, idsFlat)
			if err != nil {
				return miniLMTensorBatch{}, err
			}
			attentionMask, err := ort.NewTensor(shape, maskFlat)
			if err != nil {
				inputIDs.Destroy()
				return miniLMTensorBatch{}, err
			}
			tokenTypeIDs, err := ort.NewTensor(shape, typeFlat)
			if err != nil {
				inputIDs.Destroy()
				attentionMask.Destroy()
				return miniLMTensorBatch{}, err
			}
			return miniLMTensorBatch{
				shape:         shape,
				inputIDs:      inputIDs,
				attentionMask: attentionMask,
				tokenTypeIDs:  tokenTypeIDs,
			}, nil
		},
		SessionCheckout: func() (*ort.DynamicAdvancedSession, error) {
			return <-e.sessions, nil
		},
		ONNXRun: func(session *ort.DynamicAdvancedSession, tensors miniLMTensorBatch) (miniLMRunOutput, error) {
			defer func() {
				tensors.inputIDs.Destroy()
				tensors.attentionMask.Destroy()
				tensors.tokenTypeIDs.Destroy()
				e.sessions <- session
			}()
			outputs := []ort.Value{nil}
			if err := session.Run([]ort.Value{tensors.inputIDs, tensors.attentionMask, tensors.tokenTypeIDs}, outputs); err != nil {
				return miniLMRunOutput{}, err
			}
			outputTensor, ok := outputs[0].(*ort.Tensor[float32])
			if !ok || outputTensor == nil {
				return miniLMRunOutput{}, fmt.Errorf("unexpected model output type")
			}
			defer outputTensor.Destroy()
			data := append([]float32(nil), outputTensor.GetData()...)
			shape := append(ort.Shape(nil), outputTensor.GetShape()...)
			return miniLMRunOutput{data: data, shape: shape}, nil
		},
		PoolNormalize: func(output miniLMRunOutput, tokenized miniLMTokenizedBatch) ([][]float32, error) {
			return meanPoolNormalizeBatch(output.data, output.shape, tokenized.encodedMasks)
		},
	})
}

func (e *miniLMEmbedder) Embed(text string) ([]float32, error) {
	if vector, ok := e.embeddingCache.get(text); ok {
		return vector, nil
	}
	profiler := newEmbeddedProfiler(nil)
	defer profiler.begin("embed_total")()
	vectors, err := miniLMEmbedBatchInternalFunc(e, []string{text}, profiler)
	if err != nil {
		return nil, err
	}
	if len(vectors) != 1 {
		return nil, fmt.Errorf("unexpected batch size %d", len(vectors))
	}
	e.embeddingCache.put(text, vectors[0])
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
	runtimeSpec, err := currentORTRuntimeSpec(useGPU)
	if err != nil {
		return err
	}
	libPath := filepath.Join(modelDir, runtimeSubdir(useGPU), runtimeSpec.libraryName)
	if _, err := os.Stat(libPath); err == nil {
		return nil
	}
	return downloadAndExtractORT(filepath.Join(modelDir, runtimeSubdir(useGPU)), runtimeSpec)
}

func initializeORT(libraryPath string) error {
	ortInitOnce.Do(func() {
		ort.SetSharedLibraryPath(libraryPath)
		ortInitErr = ort.InitializeEnvironment()
	})
	return ortInitErr
}

func currentORTRuntimeSpec(useGPU bool) (ortRuntimeSpec, error) {
	return ortRuntimeSpecForPlatform(runtime.GOOS, runtime.GOARCH, useGPU)
}

func ortRuntimeSpecForPlatform(goos, goarch string, useGPU bool) (ortRuntimeSpec, error) {
	if goos != "linux" {
		return ortRuntimeSpec{}, fmt.Errorf("embedded ONNX runtime currently supports linux containers only")
	}
	switch goarch {
	case "amd64":
		if useGPU {
			return ortRuntimeSpec{url: ortLinuxAMD64GPUURL, libraryName: ortLibraryLinuxAMD64}, nil
		}
		return ortRuntimeSpec{url: ortLinuxAMD64CPUURL, libraryName: ortLibraryLinuxAMD64}, nil
	case "arm64":
		if useGPU {
			return ortRuntimeSpec{}, fmt.Errorf("embedded CUDA runtime is currently supported on linux/amd64 only")
		}
		return ortRuntimeSpec{url: ortLinuxARM64CPUURL, libraryName: ortLibraryLinuxARM64}, nil
	default:
		return ortRuntimeSpec{}, fmt.Errorf("embedded ONNX runtime currently supports linux/amd64 and linux/arm64 only")
	}
}

func downloadAndExtractORT(runtimeDir string, spec ortRuntimeSpec) error {
	response, err := http.Get(spec.url)
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
	if _, ok := libFiles[spec.libraryName]; !ok {
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
