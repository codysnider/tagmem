package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/codysnider/tagmem/internal/daemon"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

type App struct {
	stdout io.Writer
	stderr io.Writer
}

var resolveProviderFunc = vector.ProviderFromEnv
var daemonCallFunc = daemon.Call

const daemonCLIRequestTimeout = 10 * time.Second

func New(stdout, stderr io.Writer) *App {
	return &App{stdout: stdout, stderr: stderr}
}

func (a *App) Run(args []string) int {
	profiler := newProfiler(a.stderr)
	if len(args) > 0 {
		profiler.setCommand(args[0])
	}

	exitCode := 0
	defer func() {
		profiler.print(exitCode)
	}()

	done := profiler.startPhase("resolve_paths")
	paths, err := xdg.Resolve("tagmem")
	done()
	if err != nil {
		fmt.Fprintf(a.stderr, "resolve paths: %v\n", err)
		exitCode = 1
		return exitCode
	}

	done = profiler.startPhase("ensure_storage")
	if err := paths.Ensure(); err != nil {
		done()
		fmt.Fprintf(a.stderr, "prepare storage: %v\n", err)
		exitCode = 1
		return exitCode
	}
	done()

	if len(args) == 0 {
		a.printHelp()
		return exitCode
	}

	command := args[0]
	commandArgs := args[1:]

	runCommand := func(fn func() int) int {
		done := profiler.startPhase("command")
		defer done()
		return fn()
	}

	var provider vector.Provider
	var repo *store.Repository
	resolveStoreDependencies := func() bool {
		if repo != nil {
			return true
		}

		done := profiler.startPhase("resolve_provider")
		providerValue, err := resolveProviderFunc(paths)
		done()
		if err != nil {
			fmt.Fprintf(a.stderr, "resolve embedding provider: %v\n", err)
			return false
		}

		provider = providerValue
		repo = store.NewRepository(paths.StorePath, provider.IndexPath(paths.IndexDir), provider)
		attachRepoProfiler(repo, profiler)
		return true
	}

	switch command {
	case "help", "--help", "-h":
		exitCode = runCommand(func() int {
			a.printHelp()
			return 0
		})
		return exitCode
	case "init":
		exitCode = runCommand(func() int {
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runInit(repo, paths, provider)
		})
		return exitCode
	case "ingest":
		exitCode = runCommand(func() int {
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runIngest(repo, provider, commandArgs)
		})
		return exitCode
	case "split":
		exitCode = runCommand(func() int { return a.runSplit(commandArgs) })
		return exitCode
	case "add":
		exitCode = runCommand(func() int {
			if ok := preflightAddArgs(commandArgs, a.stderr); !ok {
				return 1
			}
			useDaemonCLI, daemonCLIError := selectDaemonCLIBackend(command, paths.SocketPath)
			if daemonCLIError != nil {
				fmt.Fprintf(a.stderr, "%v\n", daemonCLIError)
				return 1
			}
			if useDaemonCLI {
				return a.runAddViaDaemon(paths.SocketPath, commandArgs)
			}
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runAdd(repo, commandArgs, profiler)
		})
		return exitCode
	case "list":
		exitCode = runCommand(func() int {
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runList(repo, commandArgs)
		})
		return exitCode
	case "search":
		exitCode = runCommand(func() int {
			if ok := preflightSearchArgs(commandArgs, a.stderr); !ok {
				return 1
			}
			useDaemonCLI, daemonCLIError := selectDaemonCLIBackend(command, paths.SocketPath)
			if daemonCLIError != nil {
				fmt.Fprintf(a.stderr, "%v\n", daemonCLIError)
				return 1
			}
			if useDaemonCLI {
				return a.runSearchViaDaemon(paths.SocketPath, commandArgs)
			}
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runSearch(repo, commandArgs, profiler)
		})
		return exitCode
	case "context":
		exitCode = runCommand(func() int {
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runContext(repo, paths, commandArgs)
		})
		return exitCode
	case "status":
		exitCode = runCommand(func() int {
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runStatus(repo)
		})
		return exitCode
	case "show":
		exitCode = runCommand(func() int {
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runShow(repo, commandArgs)
		})
		return exitCode
	case "depths":
		exitCode = runCommand(func() int {
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runDepths(repo)
		})
		return exitCode
	case "paths":
		exitCode = runCommand(func() int {
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runPaths(paths, provider)
		})
		return exitCode
	case "doctor":
		exitCode = runCommand(func() int {
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runDoctor(paths, provider)
		})
		return exitCode
	case "serve":
		exitCode = runCommand(func() int {
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runServe(repo, paths, provider)
		})
		return exitCode
	case "repair":
		exitCode = runCommand(func() int {
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runRepair(repo)
		})
		return exitCode
	case "mcp":
		exitCode = runCommand(func() int {
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runMCP(repo, paths, provider)
		})
		return exitCode
	case "bench":
		exitCode = runCommand(func() int {
			if !resolveStoreDependencies() {
				return 1
			}
			return a.runBench(commandArgs, provider)
		})
		return exitCode
	default:
		fmt.Fprintf(a.stderr, "unknown command %q\n\n", command)
		a.printHelp()
		exitCode = 1
		return exitCode
	}
}

func daemonCLICommand(command string) bool {
	switch command {
	case "add", "search":
		return true
	default:
		return false
	}
}

func preflightAddArgs(args []string, stderr io.Writer) bool {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Int("depth", 1, "entry depth")
	fs.String("title", "", "entry title")
	fs.String("body", "", "entry body")
	fs.String("tags", "", "comma-separated tags")
	fs.String("source", "", "verbatim source material")
	fs.String("origin", "", "source provenance or path")
	return fs.Parse(args) == nil
}

func preflightSearchArgs(args []string, stderr io.Writer) bool {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Int("depth", -1, "filter to one depth")
	fs.Int("limit", 5, "maximum entries to show")
	fs.Bool("explain", false, "include computed support and conflict signals")
	if err := fs.Parse(args); err != nil {
		return false
	}
	if strings.TrimSpace(strings.Join(fs.Args(), " ")) == "" {
		fmt.Fprintln(stderr, "usage: tagmem search [--depth N] [--limit N] [--explain] <query>")
		return false
	}
	return true
}

func (a *App) runInit(repo *store.Repository, paths xdg.Paths, provider vector.Provider) int {
	if err := repo.Init(); err != nil {
		fmt.Fprintf(a.stderr, "initialize store: %v\n", err)
		return 1
	}

	fmt.Fprintf(a.stdout, "tagmem initialized\n")
	fmt.Fprintf(a.stdout, "data:   %s\n", paths.DataDir)
	fmt.Fprintf(a.stdout, "config: %s\n", paths.ConfigDir)
	fmt.Fprintf(a.stdout, "cache:  %s\n", paths.CacheDir)
	fmt.Fprintf(a.stdout, "index:  %s\n", provider.IndexPath(paths.IndexDir))
	fmt.Fprintf(a.stdout, "embed:  %s\n", provider.Description)
	fmt.Fprintf(a.stdout, "store:  %s\n", paths.StorePath)
	return 0
}

func (a *App) runAdd(repo *store.Repository, args []string, profiler *profiler) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	depth := fs.Int("depth", 1, "entry depth")
	title := fs.String("title", "", "entry title")
	body := fs.String("body", "", "entry body")
	tags := fs.String("tags", "", "comma-separated tags")
	source := fs.String("source", "", "verbatim source material")
	origin := fs.String("origin", "", "source provenance or path")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	done := profiler.startPhase("repo_init")
	if err := repo.Init(); err != nil {
		done()
		fmt.Fprintf(a.stderr, "initialize store: %v\n", err)
		return 1
	}
	done()

	done = profiler.startPhase("add_total")
	entry, err := repo.Add(store.AddEntry{
		Depth:     *depth,
		Title:     *title,
		Body:      *body,
		Tags:      parseCSV(*tags),
		Source:    *source,
		Origin:    *origin,
		CreatedAt: parseEnvTime("TAGMEM_IMPORT_CREATED_AT"),
		UpdatedAt: parseEnvTime("TAGMEM_IMPORT_UPDATED_AT"),
	})
	done()
	if err != nil {
		fmt.Fprintf(a.stderr, "add entry: %v\n", err)
		return 1
	}

	fmt.Fprintf(a.stdout, "added entry %d at depth %d\n", entry.ID, entry.Depth)
	return 0
}

func (a *App) runAddViaDaemon(socketPath string, args []string) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	depth := fs.Int("depth", 1, "entry depth")
	title := fs.String("title", "", "entry title")
	body := fs.String("body", "", "entry body")
	tags := fs.String("tags", "", "comma-separated tags")
	source := fs.String("source", "", "verbatim source material")
	origin := fs.String("origin", "", "source provenance or path")

	if err := fs.Parse(args); err != nil {
		return 1
	}

	entry, err := addViaDaemon(socketPath, store.AddEntry{
		Depth:     *depth,
		Title:     *title,
		Body:      *body,
		Tags:      parseCSV(*tags),
		Source:    *source,
		Origin:    *origin,
		CreatedAt: parseEnvTime("TAGMEM_IMPORT_CREATED_AT"),
		UpdatedAt: parseEnvTime("TAGMEM_IMPORT_UPDATED_AT"),
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

func (a *App) runSearch(repo *store.Repository, args []string, profiler *profiler) int {
	results, explain, err := a.querySearch(repo, args, profiler)
	if err != nil {
		fmt.Fprintf(a.stderr, "%v\n", err)
		return 1
	}

	if len(results) == 0 {
		fmt.Fprintln(a.stdout, "no matches")
		return 0
	}

	for _, result := range results {
		fmt.Fprintln(a.stdout, formatSearchResultLine(result, explain))
	}

	return 0
}

func (a *App) runSearchViaDaemon(socketPath string, args []string) int {
	results, explain, err := searchViaDaemon(socketPath, args, a.stderr)
	if err != nil {
		fmt.Fprintf(a.stderr, "%v\n", err)
		return 1
	}

	if len(results) == 0 {
		fmt.Fprintln(a.stdout, "no matches")
		return 0
	}

	for _, result := range results {
		fmt.Fprintln(a.stdout, formatSearchResultLine(result, explain))
	}

	return 0
}

func selectDaemonCLIBackend(command, socketPath string) (bool, error) {
	if !daemonCLICommand(command) {
		return false, nil
	}
	if strings.TrimSpace(os.Getenv("TAGMEM_USE_DAEMON")) != "1" {
		return false, nil
	}
	if !probeDaemonCLI(socketPath) {
		return false, fmt.Errorf("daemon-backed CLI mode requires a reachable daemon at %s", socketPath)
	}
	return true, nil
}

func probeDaemonCLI(socketPath string) bool {
	if strings.TrimSpace(os.Getenv("TAGMEM_USE_DAEMON")) != "1" {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	response, err := daemon.Call(ctx, socketPath, daemon.Request{ID: "cli-probe", Command: "status"})
	if err != nil {
		return false
	}

	return response.Success
}

func addViaDaemon(socketPath string, entry store.AddEntry) (store.Entry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), daemonCLIRequestTimeout)
	defer cancel()

	response, err := daemonCallFunc(ctx, socketPath, daemon.Request{
		ID:      fmt.Sprintf("cli-add-%d", time.Now().UnixNano()),
		Command: "add_entry",
		Payload: map[string]any{
			"depth":      entry.Depth,
			"title":      entry.Title,
			"body":       entry.Body,
			"tags":       entry.Tags,
			"source":     entry.Source,
			"origin":     entry.Origin,
			"created_at": formatOptionalTime(entry.CreatedAt),
			"updated_at": formatOptionalTime(entry.UpdatedAt),
		},
	})
	if err != nil {
		return store.Entry{}, err
	}
	if !response.Success {
		return store.Entry{}, errors.New(response.Error)
	}

	var payload struct {
		Entry store.Entry `json:"entry"`
	}
	if err := daemon.DecodePayload(response.Payload, &payload); err != nil {
		return store.Entry{}, err
	}

	return payload.Entry, nil
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func searchViaDaemon(socketPath string, args []string, stderr io.Writer) ([]store.SearchResult, bool, error) {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(stderr)

	depth := fs.Int("depth", -1, "filter to one depth")
	limit := fs.Int("limit", 5, "maximum entries to show")
	explain := fs.Bool("explain", false, "include computed support and conflict signals")

	if err := fs.Parse(args); err != nil {
		return nil, false, err
	}

	query := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(query) == "" {
		return nil, false, errors.New("usage: tagmem search [--depth N] [--limit N] [--explain] <query>")
	}

	payload := map[string]any{
		"query": query,
		"limit": *limit,
	}
	if *depth >= 0 {
		payload["depth"] = *depth
	}

	ctx, cancel := context.WithTimeout(context.Background(), daemonCLIRequestTimeout)
	defer cancel()

	response, err := daemonCallFunc(ctx, socketPath, daemon.Request{
		ID:      fmt.Sprintf("cli-search-%d", time.Now().UnixNano()),
		Command: "search",
		Payload: payload,
	})
	if err != nil {
		return nil, false, err
	}
	if !response.Success {
		return nil, false, errors.New(response.Error)
	}

	var decoded struct {
		Results []store.SearchResult `json:"results"`
	}
	if err := daemon.DecodePayload(response.Payload, &decoded); err != nil {
		return nil, false, err
	}

	return decoded.Results, *explain, nil
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
	if entry.Origin != "" {
		fmt.Fprintf(a.stdout, "Origin: %s\n", entry.Origin)
	}
	fmt.Fprintf(a.stdout, "Created: %s\n", entry.CreatedAt.Format(timeFormat))
	fmt.Fprintf(a.stdout, "Updated: %s\n", entry.UpdatedAt.Format(timeFormat))
	fmt.Fprintln(a.stdout, "")
	fmt.Fprintln(a.stdout, "Body:")
	fmt.Fprintln(a.stdout, entry.Body)
	if entry.Source != "" {
		fmt.Fprintln(a.stdout, "")
		fmt.Fprintln(a.stdout, "Source:")
		fmt.Fprintln(a.stdout, entry.Source)
	}
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

func (a *App) querySearch(repo *store.Repository, args []string, profiler *profiler) ([]store.SearchResult, bool, error) {
	fs := flag.NewFlagSet("search", flag.ContinueOnError)
	fs.SetOutput(a.stderr)

	depth := fs.Int("depth", -1, "filter to one depth")
	limit := fs.Int("limit", 5, "maximum entries to show")
	explain := fs.Bool("explain", false, "include computed support and conflict signals")

	if err := fs.Parse(args); err != nil {
		return nil, false, err
	}

	done := profiler.startPhase("repo_init")
	if err := repo.Init(); err != nil {
		done()
		return nil, false, fmt.Errorf("initialize store: %w", err)
	}
	done()

	query := store.Query{Limit: *limit, Text: strings.Join(fs.Args(), " ")}
	if *depth >= 0 {
		query.Depth = depth
	}
	if strings.TrimSpace(query.Text) == "" {
		return nil, false, errors.New("usage: tagmem search [--depth N] [--limit N] [--explain] <query>")
	}

	done = profiler.startPhase("search_total")
	results, err := repo.SearchDetailed(query)
	done()
	if err != nil {
		return nil, false, err
	}
	return results, *explain, nil
}

func attachRepoProfiler(repo *store.Repository, profiler *profiler) {
	if !profiler.enabled {
		return
	}

	repo.SetPhaseHook(func(name string, duration time.Duration) {
		profiler.phases = append(profiler.phases, profilePhase{name: name, duration: duration})
	})
}

func (a *App) printHelp() {
	fmt.Fprintln(a.stdout, "tagmem")
	fmt.Fprintln(a.stdout, "")
	fmt.Fprintln(a.stdout, "Commands:")
	fmt.Fprintln(a.stdout, "  init                 optionally precreate local storage")
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
	fmt.Fprintln(a.stdout, "  serve                run local daemon over a Unix socket")
	fmt.Fprintln(a.stdout, "  repair               rebuild the vector index from stored entries")
	fmt.Fprintln(a.stdout, "  mcp                  run MCP server over stdio")
	fmt.Fprintln(a.stdout, "  bench                run perf, longmemeval, locomo, convomem, or suite")
	fmt.Fprintln(a.stdout, "")
	fmt.Fprintln(a.stdout, "Run `tagmem help` to see this list again.")
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

	if entry.Origin != "" {
		parts = append(parts, "origin="+entry.Origin)
	}

	return strings.Join(parts, "  ")
}

func formatSearchResultLine(result store.SearchResult, explain bool) string {
	parts := []string{formatEntryLine(result.Entry)}
	if explain {
		parts = append(parts,
			fmt.Sprintf("support=%d", result.SupportCount),
			fmt.Sprintf("sources=%d", result.SourceKinds),
			fmt.Sprintf("conflicts=%d", result.ConflictCount),
		)
	}
	return strings.Join(parts, "  ")
}

const timeFormat = time.RFC3339

func parseEnvTime(key string) *time.Time {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	return &parsed
}
