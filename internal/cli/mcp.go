package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/codysnider/tagmem/internal/diary"
	"github.com/codysnider/tagmem/internal/kg"
	"github.com/codysnider/tagmem/internal/mcp"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

func (a *App) runMCP(repo *store.Repository, paths xdg.Paths, provider vector.Provider) int {
	if err := repo.Init(); err != nil {
		fmt.Fprintf(a.stderr, "initialize store: %v\n", err)
		return 1
	}

	server := mcp.New(os.Stdin, a.stdout, a.stderr, repo, kg.New(paths.KGPath), diary.New(paths.DiaryDir), paths, provider)
	if err := server.Run(context.Background()); err != nil {
		fmt.Fprintf(a.stderr, "run mcp server: %v\n", err)
		return 1
	}
	return 0
}
