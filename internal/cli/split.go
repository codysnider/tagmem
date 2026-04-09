package cli

import (
	"flag"
	"fmt"

	"github.com/codysnider/tagmem/internal/splitter"
)

func (a *App) runSplit(args []string) int {
	fs := flag.NewFlagSet("split", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	outputDir := fs.String("output-dir", "", "write split files here")
	dryRun := fs.Bool("dry-run", false, "show what would be split without writing")
	minSessions := fs.Int("min-sessions", 2, "only split files with at least N sessions")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.stderr, "usage: tagmem split [--output-dir DIR] [--dry-run] [--min-sessions N] <dir>")
		return 1
	}
	result, err := splitter.Run(fs.Arg(0), *outputDir, *minSessions, *dryRun)
	if err != nil {
		fmt.Fprintf(a.stderr, "split: %v\n", err)
		return 1
	}
	fmt.Fprintf(a.stdout, "files scanned: %d\n", result.FilesScanned)
	fmt.Fprintf(a.stdout, "mega-files:    %d\n", result.MegaFiles)
	fmt.Fprintf(a.stdout, "sessions:      %d\n", result.Sessions)
	return 0
}
