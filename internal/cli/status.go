package cli

import (
	"fmt"
	"sort"

	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/taggraph"
)

func (a *App) runStatus(repo *store.Repository) int {
	entries, err := repo.List(store.Query{Limit: 0})
	if err != nil {
		fmt.Fprintf(a.stderr, "status: %v\n", err)
		return 1
	}
	depths, err := repo.DepthCounts()
	if err != nil {
		fmt.Fprintf(a.stderr, "status: %v\n", err)
		return 1
	}
	tags := taggraph.TagCounts(entries)
	fmt.Fprintf(a.stdout, "entries: %d\n", len(entries))
	fmt.Fprintln(a.stdout, "depths:")
	for _, depth := range depths {
		fmt.Fprintf(a.stdout, "  depth %d: %d\n", depth.Depth, depth.Count)
	}
	if len(tags) > 0 {
		type item struct {
			name  string
			count int
		}
		items := make([]item, 0, len(tags))
		for name, count := range tags {
			items = append(items, item{name, count})
		}
		sort.Slice(items, func(i, j int) bool {
			if items[i].count == items[j].count {
				return items[i].name < items[j].name
			}
			return items[i].count > items[j].count
		})
		fmt.Fprintln(a.stdout, "top tags:")
		for i, item := range items {
			if i >= 10 {
				break
			}
			fmt.Fprintf(a.stdout, "  %s: %d\n", item.name, item.count)
		}
	}
	return 0
}
