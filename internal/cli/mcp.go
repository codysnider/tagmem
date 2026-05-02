package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/codysnider/tagmem/internal/daemon"
	"github.com/codysnider/tagmem/internal/diary"
	"github.com/codysnider/tagmem/internal/kg"
	"github.com/codysnider/tagmem/internal/mcp"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

func (a *App) runMCP(repo *store.Repository, paths xdg.Paths, provider vector.Provider) int {
	var server *mcp.Server
	useDaemon, err := selectDaemonMCPBackend(paths.SocketPath)
	if err != nil {
		fmt.Fprintf(a.stderr, "%v\n", err)
		return 1
	}
	if useDaemon {
		server = mcp.NewWithDaemonSocket(os.Stdin, a.stdout, a.stderr, paths.SocketPath, repo, kg.New(paths.KGPath), diary.New(paths.DiaryDir), paths, provider)
	} else {
		if err := repo.Init(); err != nil {
			fmt.Fprintf(a.stderr, "initialize store: %v\n", err)
			return 1
		}
		server = mcp.New(os.Stdin, a.stdout, a.stderr, repo, kg.New(paths.KGPath), diary.New(paths.DiaryDir), paths, provider)
	}

	if err := server.Run(context.Background()); err != nil {
		fmt.Fprintf(a.stderr, "run mcp server: %v\n", err)
		return 1
	}
	return 0
}

func selectDaemonMCPBackend(socketPath string) (bool, error) {
	if daemonMCPEnabledFromEnv() {
		if !probeDaemonMCPBackend(socketPath) {
			return false, fmt.Errorf("daemon-backed MCP mode requires a reachable daemon at %s", socketPath)
		}
		return true, nil
	}

	return shouldUseDaemonMCPBackend(socketPath), nil
}

func shouldUseDaemonMCPBackend(socketPath string) bool {
	if !daemonMCPEnabledFromEnv() {
		info, err := os.Stat(socketPath)
		if err != nil {
			return false
		}
		if info.Mode()&os.ModeSocket == 0 {
			return false
		}
	}
	return probeDaemonMCPBackend(socketPath)
}

func daemonMCPEnabledFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TAGMEM_MCP_USE_DAEMON"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func probeDaemonMCPBackend(socketPath string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	response, err := daemon.Call(ctx, socketPath, daemon.Request{ID: "mcp-probe", Command: "status"})
	if err != nil {
		return false
	}
	return response.Success
}
