package cli

import (
	"fmt"

	"github.com/codysnider/tagmem/internal/store"
)

func (a *App) runRepair(repo *store.Repository) int {
	if err := repo.RebuildIndex(); err != nil {
		fmt.Fprintf(a.stderr, "repair: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.stdout, "vector index rebuilt")
	return 0
}
