package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/codysnider/tagmem/internal/daemon"
	"github.com/codysnider/tagmem/internal/diary"
	"github.com/codysnider/tagmem/internal/kg"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

var serveDaemonFunc = startDaemonServer
var signalContextFunc = func(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}
var runDaemonServerFunc = func(ctx context.Context, socketPath string, repo *store.Repository, paths xdg.Paths, provider vector.Provider) error {
	backend := daemon.NewBackend(repo, kg.New(paths.KGPath), diary.New(paths.DiaryDir), paths, provider)
	server := daemon.NewServer(socketPath, backend)
	return server.Run(ctx)
}

func (a *App) runServe(repo *store.Repository, paths xdg.Paths, provider vector.Provider) int {
	if err := serveDaemonFunc(repo, paths, provider); err != nil {
		fmt.Fprintf(a.stderr, "serve: %v\n", err)
		return 1
	}
	return 0
}

func startDaemonServer(repo *store.Repository, paths xdg.Paths, provider vector.Provider) error {
	if err := repo.Init(); err != nil {
		return fmt.Errorf("initialize store: %w", err)
	}

	ctx, stop := signalContextFunc(context.Background())
	defer stop()

	return runDaemonServerFunc(ctx, paths.SocketPath, repo, paths, provider)
}
