package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/ui"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

type App struct {
	stdout io.Writer
	stderr io.Writer
}

func New(stdout, stderr io.Writer) *App {
	return &App{stdout: stdout, stderr: stderr}
}

func (a *App) Run(args []string) int {
	paths, err := xdg.Resolve("tagmem")
	if err != nil {
		fmt.Fprintf(a.stderr, "resolve paths: %v\n", err)
		return 1
	}

	if err := paths.Ensure(); err != nil {
		fmt.Fprintf(a.stderr, "prepare storage: %v\n", err)
		return 1
	}

	provider, err := vector.ProviderFromEnv(paths)
	if err != nil {
		fmt.Fprintf(a.stderr, "resolve embedding provider: %v\n", err)
		return 1
	}

	indexPath := provider.IndexPath(paths.IndexDir)
	repo := store.NewRepository(paths.StorePath, indexPath, provider)

	if len(args) == 0 {
		if err := repo.Init(); err != nil {
			fmt.Fprintf(a.stderr, "initialize store: %v\n", err)
			return 1
		}
		if err := ui.Run(repo, paths, provider); err != nil {
			fmt.Fprintf(a.stderr, "run tui: %v\n", err)
			return 1
		}
		return 0
	}

	command := args[0]
	commandArgs := args[1:]

	switch command {
	case "help", "--help", "-h":
		a.printHelp()
		return 0
	case "init":
		return a.runInit(repo, paths, provider)
	case "ingest":
		return a.runIngest(repo, provider, commandArgs)
	case "split":
		return a.runSplit(commandArgs)
	case "add":
		return a.runAdd(repo, commandArgs)
	case "list":
		return a.runList(repo, commandArgs)
	case "search":
		return a.runSearch(repo, commandArgs)
	case "context":
		return a.runContext(repo, paths, commandArgs)
	case "status":
		return a.runStatus(repo)
	case "show":
		return a.runShow(repo, commandArgs)
	case "depths":
		return a.runDepths(repo)
	case "paths":
		return a.runPaths(paths, provider)
	case "doctor":
		return a.runDoctor(paths, provider)
	case "repair":
		return a.runRepair(repo)
	case "mcp":
		return a.runMCP(repo, paths, provider)
	case "bench":
		return a.runBench(commandArgs, provider)
	case "tui":
		if err := repo.Init(); err != nil {
			fmt.Fprintf(a.stderr, "initialize store: %v\n", err)
			return 1
		}
		if err := ui.Run(repo, paths, provider); err != nil {
			fmt.Fprintf(a.stderr, "run tui: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(a.stderr, "unknown command %q\n\n", command)
		a.printHelp()
		return 1
	}
}

func (a *App) runInit(repo *store.Repository, paths xdg.Paths, provider vector.Provider) int {
	if err := repo.Init(); err != nil {
		fmt.Fprintf(a.stderr, "initialize store: %v\n", err)
		return 1
	}

	fmt.Fprintf(a.stdout, "tiered-memory initialized\n")
	fmt.Fprintf(a.stdout, "data:   %s\n", paths.DataDir)
	fmt.Fprintf(a.stdout, "config: %s\n", paths.ConfigDir)
	fmt.Fprintf(a.stdout, "cache:  %s\n", paths.CacheDir)
	fmt.Fprintf(a.stdout, "index:  %s\n", provider.IndexPath(paths.IndexDir))
	fmt.Fprintf(a.stdout, "embed:  %s\n", provider.Description)
	fmt.Fprintf(a.stdout, "store:  %s\n", paths.StorePath)
	return 0
}

func (a *App) runAdd(repo *store.Repository, args []string) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	depth := fs.Int("depth", 1, "entry depth")
	title := fs.String("title", "", "entry title")
	body := fs.String("body", "", "entry body")
	tags := fs.String("tags", "", "comma-separated tags")
	source := fs.String("source", "", "entry source")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	if err := repo.Init(); err != nil {
		fmt.Fprintf(a.stderr, "initialize store: %v\n", err)
		return 1
	}

	entry, err := repo.Add(store.AddEntry{
		Depth:  *depth,
		Title:  *title,
		Body:   *body,
		Tags:   parseCSV(*tags),
		Source: *source,
	})
	if err != nil {
		fmt.Fprintf(a.stderr, "add entry: %v\n", err)
		return 1
	}

	fmt.Fprintf(a.stdout, "added entry %d at depth %d\n", entry.ID, entry.Depth)
	return 0
}

func (a *App) runList(repo *store.Repository, args []string) int {
	entries, err := a.queryEntries(repo, args, false)
	if err != nil {
		fmt.Fprintf(a.stderr, "%v\n", err)
		return 1
	}

	if len(entries) == 0 {
		fmt.Fprintln(a.stdout, "no entries")
		return 0
	}

	for _, entry := range entries {
		fmt.Fprintln(a.stdout, formatEntryLine(entry))
	}

	return 0
}

func (a *App) runSearch(repo *store.Repository, args []string) int {
	entries, err := a.queryEntries(repo, args, true)
	if err != nil {
		fmt.Fprintf(a.stderr, "%v\n", err)
		return 1
	}

	if len(entries) == 0 {
		fmt.Fprintln(a.stdout, "no matches")
		return 0
	}

	for _, entry := range entries {
		fmt.Fprintln(a.stdout, formatEntryLine(entry))
	}

	return 0
}

func (a *App) runShow(repo *store.Repository, args []string) int {
	if len(args) != 1 {
		fmt.Fprintln(a.stderr, "usage: tagmem show <id>")
		return 1
	}

	id, err := strconv.Atoi(args[0])
	if err != nil {
		fmt.Fprintf(a.stderr, "invalid id %q\n", args[0])
		return 1
	}

	if err := repo.Init(); err != nil {
		fmt.Fprintf(a.stderr, "initialize store: %v\n", err)
		return 1
	}

	entry, ok, err := repo.Get(id)
	if err != nil {
		fmt.Fprintf(a.stderr, "load entry: %v\n", err)
		return 1
	}
	if !ok {
		fmt.Fprintf(a.stderr, "entry %d not found\n", id)
		return 1
	}

	fmt.Fprintf(a.stdout, "ID: %d\n", entry.ID)
	fmt.Fprintf(a.stdout, "Depth: %d\n", entry.Depth)
	fmt.Fprintf(a.stdout, "Title: %s\n", entry.Title)
	if len(entry.Tags) > 0 {
		fmt.Fprintf(a.stdout, "Tags: %s\n", strings.Join(entry.Tags, ", "))
	}
	if entry.Source != "" {
		fmt.Fprintf(a.stdout, "Source: %s\n", entry.Source)
	}
	fmt.Fprintf(a.stdout, "Created: %s\n", entry.CreatedAt.Format(timeFormat))
	fmt.Fprintf(a.stdout, "Updated: %s\n", entry.UpdatedAt.Format(timeFormat))
	fmt.Fprintln(a.stdout, "")
	fmt.Fprintln(a.stdout, entry.Body)
	return 0
}

func (a *App) runDepths(repo *store.Repository) int {
	if err := repo.Init(); err != nil {
		fmt.Fprintf(a.stderr, "initialize store: %v\n", err)
		return 1
	}

	summaries, err := repo.DepthCounts()
	if err != nil {
		fmt.Fprintf(a.stderr, "load depth counts: %v\n", err)
		return 1
	}

	if len(summaries) == 0 {
		fmt.Fprintln(a.stdout, "no depths yet")
		return 0
	}

	for _, summary := range summaries {
		fmt.Fprintf(a.stdout, "depth %d\t%d\n", summary.Depth, summary.Count)
	}

	return 0
}

func (a *App) runPaths(paths xdg.Paths, provider vector.Provider) int {
	fmt.Fprintf(a.stdout, "data:   %s\n", paths.DataDir)
	fmt.Fprintf(a.stdout, "config: %s\n", paths.ConfigDir)
	fmt.Fprintf(a.stdout, "cache:  %s\n", paths.CacheDir)
	fmt.Fprintf(a.stdout, "index:  %s\n", provider.IndexPath(paths.IndexDir))
	fmt.Fprintf(a.stdout, "embed:  %s\n", provider.Description)
	fmt.Fprintf(a.stdout, "store:  %s\n", paths.StorePath)
	return 0
}

func (a *App) queryEntries(repo *store.Repository, args []string, useSearch bool) ([]store.Entry, error) {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	depth := fs.Int("depth", -1, "filter to one depth")
	defaultLimit := 25
	if useSearch {
		defaultLimit = 5
	}
	limit := fs.Int("limit", defaultLimit, "maximum entries to show")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if err := repo.Init(); err != nil {
		return nil, fmt.Errorf("initialize store: %w", err)
	}

	query := store.Query{Limit: *limit}
	if *depth >= 0 {
		query.Depth = depth
	}

	if useSearch {
		query.Text = strings.Join(fs.Args(), " ")
		if strings.TrimSpace(query.Text) == "" {
			return nil, errors.New("usage: tagmem search [--depth N] [--limit N] <query>")
		}
		return repo.Search(query)
	}

	if len(fs.Args()) > 0 {
		return nil, errors.New("usage: tagmem list [--depth N] [--limit N]")
	}

	return repo.List(query)
}

func (a *App) printHelp() {
	fmt.Fprintln(a.stdout, "tagmem")
	fmt.Fprintln(a.stdout, "")
	fmt.Fprintln(a.stdout, "Commands:")
	fmt.Fprintln(a.stdout, "  init                 initialize local storage")
	fmt.Fprintln(a.stdout, "  ingest               import files or conversations from a directory")
	fmt.Fprintln(a.stdout, "  split                split transcript mega-files into per-session files")
	fmt.Fprintln(a.stdout, "  add                  add a memory entry")
	fmt.Fprintln(a.stdout, "  list                 list entries")
	fmt.Fprintln(a.stdout, "  search               search entries")
	fmt.Fprintln(a.stdout, "  context              render compact always-load context")
	fmt.Fprintln(a.stdout, "  status               summarize stored entries, depths, and tags")
	fmt.Fprintln(a.stdout, "  show <id>            show one entry")
	fmt.Fprintln(a.stdout, "  depths               show depth counts")
	fmt.Fprintln(a.stdout, "  paths                print resolved storage paths")
	fmt.Fprintln(a.stdout, "  doctor               validate embedding backend")
	fmt.Fprintln(a.stdout, "  repair               rebuild the vector index from stored entries")
	fmt.Fprintln(a.stdout, "  mcp                  run MCP server over stdio")
	fmt.Fprintln(a.stdout, "  bench                run perf, longmemeval, locomo, convomem, or suite")
	fmt.Fprintln(a.stdout, "  tui                  open the terminal UI")
	fmt.Fprintln(a.stdout, "")
	fmt.Fprintln(a.stdout, "If no command is provided, the terminal UI opens.")
}

func parseCSV(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	return strings.Split(input, ",")
}

func formatEntryLine(entry store.Entry) string {
	parts := []string{
		fmt.Sprintf("[%d]", entry.ID),
		fmt.Sprintf("depth=%d", entry.Depth),
		entry.Title,
	}

	if len(entry.Tags) > 0 {
		parts = append(parts, "tags="+strings.Join(entry.Tags, ","))
	}

	return strings.Join(parts, "  ")
}

const timeFormat = time.RFC3339
