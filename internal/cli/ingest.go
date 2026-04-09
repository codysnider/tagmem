package cli

import (
	"flag"
	"fmt"
	"strings"

	"github.com/codysnider/tagmem/internal/importer"
	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/vector"
)

func (a *App) runIngest(repo *store.Repository, provider vector.Provider, args []string) int {
	fs := flag.NewFlagSet("ingest", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	mode := fs.String("mode", "files", "files or conversations")
	extract := fs.String("extract", "exchange", "exchange or general for conversations")
	depth := fs.Int("depth", 1, "target depth")
	limit := fs.Int("limit", 0, "max files to process")
	dryRun := fs.Bool("dry-run", false, "scan without writing")
	skipExisting := fs.Bool("skip-existing", true, "skip files already represented by source path")
	noGitignore := fs.Bool("no-gitignore", false, "do not respect .gitignore while scanning files")
	includeIgnored := fs.String("include-ignored", "", "comma-separated project-relative paths to include even if ignored")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.stderr, "usage: tagmem ingest [--mode files|conversations] [--depth N] [--limit N] <dir>")
		return 1
	}
	modeValue := importer.Mode(*mode)
	if modeValue != importer.ModeFiles && modeValue != importer.ModeConversations {
		fmt.Fprintf(a.stderr, "invalid mode %q\n", *mode)
		return 1
	}
	result, err := importer.Run(repo, importer.Options{SourceDir: fs.Arg(0), Mode: modeValue, Extract: *extract, Depth: *depth, Limit: *limit, DryRun: *dryRun, SkipExisting: *skipExisting, RespectGitignore: !*noGitignore, IncludeIgnored: splitCSV(*includeIgnored), Provider: &provider})
	if err != nil {
		fmt.Fprintf(a.stderr, "ingest: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.stdout, "files processed: %d\n", result.FilesProcessed)
	fmt.Fprintf(a.stdout, "files skipped:   %d\n", result.FilesSkipped)
	fmt.Fprintf(a.stdout, "entries added:   %d\n", result.EntriesAdded)
	return 0
}

func splitCSV(value string) []string {
	if value == "" {
		return nil
	}
	parts := []string{}
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}
