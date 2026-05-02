package cli

import (
	"context"
	"testing"

	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/testutil/fakeembed"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

func TestStartDaemonServerUsesSignalAwareContext(t *testing.T) {
	paths := xdg.Paths{StorePath: t.TempDir() + "/store.json", IndexDir: t.TempDir()}
	provider := fakeembed.Provider()
	repo := store.NewRepository(paths.StorePath, provider.IndexPath(paths.IndexDir), provider)

	originalSignalContextFunc := signalContextFunc
	originalRunDaemonServerFunc := runDaemonServerFunc
	defer func() {
		signalContextFunc = originalSignalContextFunc
		runDaemonServerFunc = originalRunDaemonServerFunc
	}()

	signalContextCalled := false
	signalContextFunc = func(parent context.Context) (context.Context, context.CancelFunc) {
		signalContextCalled = true
		return context.WithCancel(parent)
	}

	runDaemonServerFunc = func(ctx context.Context, _ string, _ *store.Repository, _ xdg.Paths, _ vector.Provider) error {
		if !signalContextCalled {
			t.Fatal("signalContextFunc was not called before runDaemonServerFunc")
		}
		if ctx.Done() == nil {
			t.Fatal("ctx.Done() = nil, want signal-aware cancellable context")
		}
		return nil
	}

	if err := startDaemonServer(repo, paths, provider); err != nil {
		t.Fatalf("startDaemonServer() error = %v", err)
	}
}
