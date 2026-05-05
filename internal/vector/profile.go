package vector

import (
	"log"
	"os"
	"time"
)

type embeddedProfiler struct {
	enabled bool
	sink    func(string, time.Duration)
}

type embeddedBatchProfileOps[TTokenized, TPrepared, TSession, TOutput any] struct {
	Tokenize        func([]string) (TTokenized, error)
	TensorPrepare   func(TTokenized) (TPrepared, error)
	SessionCheckout func() (TSession, error)
	ONNXRun         func(TSession, TPrepared) (TOutput, error)
	PoolNormalize   func(TOutput, TTokenized) ([][]float32, error)
}

func newEmbeddedProfiler(sink func(string, time.Duration)) embeddedProfiler {
	if sink == nil {
		sink = embeddedProfileSink
	}
	return embeddedProfiler{
		enabled: os.Getenv("TAGMEM_EMBED_PROFILE") == "1",
		sink:    sink,
	}
}

func (p embeddedProfiler) begin(name string) func() {
	if !p.enabled {
		return func() {}
	}
	started := time.Now()
	return func() {
		p.sink(name, time.Since(started))
	}
}

func runEmbeddedBatchProfiled[TTokenized, TPrepared, TSession, TOutput any](texts []string, profiler embeddedProfiler, ops embeddedBatchProfileOps[TTokenized, TPrepared, TSession, TOutput]) ([][]float32, error) {
	stopTokenize := profiler.begin("tokenize")
	tokenized, err := ops.Tokenize(texts)
	stopTokenize()
	if err != nil {
		return nil, err
	}

	stopTensorPrepare := profiler.begin("tensor_prepare")
	prepared, err := ops.TensorPrepare(tokenized)
	stopTensorPrepare()
	if err != nil {
		return nil, err
	}

	stopSessionCheckout := profiler.begin("session_checkout")
	session, err := ops.SessionCheckout()
	stopSessionCheckout()
	if err != nil {
		return nil, err
	}

	stopONNXRun := profiler.begin("onnx_run")
	output, err := ops.ONNXRun(session, prepared)
	stopONNXRun()
	if err != nil {
		return nil, err
	}

	stopPoolNormalize := profiler.begin("pool_normalize")
	vectors, err := ops.PoolNormalize(output, tokenized)
	stopPoolNormalize()
	if err != nil {
		return nil, err
	}

	return vectors, nil
}

var embeddedProfileSink = func(name string, elapsed time.Duration) {
	log.Printf("tagmem_embed_profile phase=%s elapsed=%s", name, elapsed)
}
