package cli

import (
	"flag"
	"fmt"

	"github.com/codysnider/tagmem/internal/contextview"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/xdg"
)

func (a *App) runContext(repo *store.Repository, paths xdg.Paths, args []string) int {
	fs := flag.NewFlagSet("context", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	depth := fs.Int("depth", -1, "filter to one depth")
	tag := fs.String("tag", "", "filter to one tag")
	limit := fs.Int("limit", 12, "max entries to include")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	entries, err := repo.List(store.Query{Limit: 0})
	if err != nil {
		fmt.Fprintf(a.stderr, "context: %v\n", err)
		return 1
	}
	var depthPtr *int
	if *depth >= 0 {
		depthPtr = depth
	}
	text := contextview.Render(entries, contextview.Options{IdentityPath: paths.IdentityPath, Depth: depthPtr, Tag: *tag, Limit: *limit})
	fmt.Fprintln(a.stdout, text)
	return 0
}
