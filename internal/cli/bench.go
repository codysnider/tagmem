package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/codysnider/tagmem/internal/bench"
	"github.com/codysnider/tagmem/internal/vector"
)

func (a *App) runBench(args []string, provider vector.Provider) int {
	if len(args) == 0 {
		fmt.Fprintln(a.stderr, "usage: tagmem bench <perf|longmemeval|locomo|convomem|membench|suite> [options]")
		return 1
	}

	switch args[0] {
	case "perf":
		return a.runBenchPerf(args[1:], provider)
	case "longmemeval":
		return a.runBenchLongMemEval(args[1:], provider)
	case "locomo":
		return a.runBenchLoCoMo(args[1:], provider)
	case "convomem":
		return a.runBenchConvoMem(args[1:], provider)
	case "membench":
		return a.runBenchMemBench(args[1:], provider)
	case "suite":
		return a.runBenchSuite(args[1:], provider)
	default:
		fmt.Fprintf(a.stderr, "unknown benchmark %q\n", args[0])
		return 1
	}
}

func (a *App) runBenchPerf(args []string, provider vector.Provider) int {
	fs := flag.NewFlagSet("bench perf", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	entries := fs.Int("entries", 250, "number of entries to add")
	searches := fs.Int("searches", 25, "number of searches to run")
	out := fs.String("out", "", "optional JSON output path")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	root, err := os.MkdirTemp("", "tagmem-bench-*")
	if err != nil {
		fmt.Fprintf(a.stderr, "create temp dir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(root)

	result, err := bench.RunPerf(filepath.Join(root, "perf"), *entries, *searches, provider)
	if err != nil {
		fmt.Fprintf(a.stderr, "run perf benchmark: %v\n", err)
		return 1
	}
	if err := bench.WritePerfResult(*out, result); err != nil {
		fmt.Fprintf(a.stderr, "write perf result: %v\n", err)
		return 1
	}
	fmt.Fprint(a.stdout, bench.FormatPerf(result))
	return 0
}

func (a *App) runBenchLongMemEval(args []string, provider vector.Provider) int {
	fs := flag.NewFlagSet("bench longmemeval", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	limit := fs.Int("limit", 0, "optional number of questions to run")
	out := fs.String("out", "", "optional JSON output path")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.stderr, "usage: tagmem bench longmemeval [--limit N] <data-file>")
		return 1
	}

	result, err := bench.RunLongMemEval(context.Background(), fs.Arg(0), *limit, provider)
	if err != nil {
		fmt.Fprintf(a.stderr, "run longmemeval benchmark: %v\n", err)
		return 1
	}
	if err := bench.WriteLongMemEvalResult(*out, result); err != nil {
		fmt.Fprintf(a.stderr, "write longmemeval result: %v\n", err)
		return 1
	}
	fmt.Fprint(a.stdout, bench.FormatLongMemEval(result))
	return 0
}

func (a *App) runBenchLoCoMo(args []string, provider vector.Provider) int {
	fs := flag.NewFlagSet("bench locomo", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	limit := fs.Int("limit", 0, "optional number of conversations to run")
	topK := fs.Int("top-k", 10, "retrieval top-k")
	out := fs.String("out", "", "optional JSON output path")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.stderr, "usage: tagmem bench locomo [--limit N] [--top-k N] <data-file>")
		return 1
	}

	result, err := bench.RunLoCoMo(context.Background(), fs.Arg(0), *limit, *topK, provider)
	if err != nil {
		fmt.Fprintf(a.stderr, "run locomo benchmark: %v\n", err)
		return 1
	}
	if err := bench.WriteLoCoMoResult(*out, result); err != nil {
		fmt.Fprintf(a.stderr, "write locomo result: %v\n", err)
		return 1
	}
	fmt.Fprint(a.stdout, bench.FormatLoCoMo(result))
	return 0
}

func (a *App) runBenchConvoMem(args []string, provider vector.Provider) int {
	fs := flag.NewFlagSet("bench convomem", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	limit := fs.Int("limit", 10, "items per category")
	topK := fs.Int("top-k", 10, "retrieval top-k")
	cacheDir := fs.String("cache-dir", "/tmp/convomem_cache", "cache directory")
	category := fs.String("category", "all", "category name or 'all'")
	out := fs.String("out", "", "optional JSON output path")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	categories := make([]string, 0, len(bench.ConvoMemCategories))
	if *category == "all" {
		for key := range bench.ConvoMemCategories {
			categories = append(categories, key)
		}
	} else {
		categories = append(categories, *category)
	}
	result, err := bench.RunConvoMem(context.Background(), categories, *limit, *topK, *cacheDir, provider)
	if err != nil {
		fmt.Fprintf(a.stderr, "run convomem benchmark: %v\n", err)
		return 1
	}
	if err := bench.WriteConvoMemResult(*out, result); err != nil {
		fmt.Fprintf(a.stderr, "write convomem result: %v\n", err)
		return 1
	}
	fmt.Fprint(a.stdout, bench.FormatConvoMem(result))
	return 0
}

func (a *App) runBenchMemBench(args []string, provider vector.Provider) int {
	fs := flag.NewFlagSet("bench membench", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	category := fs.String("category", "", "single category to run")
	topic := fs.String("topic", "movie", "topic filter")
	topK := fs.Int("top-k", 5, "retrieval top-k")
	limit := fs.Int("limit", 0, "limit items")
	out := fs.String("out", "", "optional JSON output path")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.stderr, "usage: tagmem bench membench [--category name] [--topic movie] [--top-k N] [--limit N] <data-dir>")
		return 1
	}
	var categories []string
	if *category != "" {
		categories = []string{*category}
	}
	result, err := bench.RunMemBench(context.Background(), fs.Arg(0), categories, *topic, *topK, *limit, provider)
	if err != nil {
		fmt.Fprintf(a.stderr, "run membench benchmark: %v\n", err)
		return 1
	}
	if err := bench.WriteMemBenchResult(*out, result); err != nil {
		fmt.Fprintf(a.stderr, "write membench result: %v\n", err)
		return 1
	}
	fmt.Fprint(a.stdout, bench.FormatMemBench(result))
	return 0
}

func (a *App) runBenchSuite(args []string, provider vector.Provider) int {
	fs := flag.NewFlagSet("bench suite", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	longFile := fs.String("longmemeval", "", "path to longmemeval json")
	locomoFile := fs.String("locomo", "", "path to locomo10.json")
	membenchDir := fs.String("membench", "", "path to MemBench FirstAgent dir")
	convomemLimit := fs.Int("convomem-limit", 0, "items per category for convomem; 0 disables")
	convomemCache := fs.String("convomem-cache-dir", "/tmp/convomem_cache", "convomem cache dir")
	perfEntries := fs.Int("perf-entries", 250, "perf benchmark entries")
	perfSearches := fs.Int("perf-searches", 25, "perf benchmark searches")
	outDir := fs.String("out-dir", "", "optional directory for JSON outputs")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	root, err := os.MkdirTemp("", "tagmem-suite-*")
	if err != nil {
		fmt.Fprintf(a.stderr, "create temp dir: %v\n", err)
		return 1
	}
	defer os.RemoveAll(root)

	perfResult, err := bench.RunPerf(filepath.Join(root, "perf"), *perfEntries, *perfSearches, provider)
	if err != nil {
		fmt.Fprintf(a.stderr, "run perf benchmark: %v\n", err)
		return 1
	}
	if *outDir != "" {
		if err := bench.WritePerfResult(filepath.Join(*outDir, "perf.json"), perfResult); err != nil {
			fmt.Fprintf(a.stderr, "write perf result: %v\n", err)
			return 1
		}
	}
	fmt.Fprintln(a.stdout, bench.FormatPerf(perfResult))

	if strings.TrimSpace(*longFile) != "" {
		result, err := bench.RunLongMemEval(context.Background(), *longFile, 0, provider)
		if err != nil {
			fmt.Fprintf(a.stderr, "run longmemeval benchmark: %v\n", err)
			return 1
		}
		if *outDir != "" {
			if err := bench.WriteLongMemEvalResult(filepath.Join(*outDir, "longmemeval.json"), result); err != nil {
				fmt.Fprintf(a.stderr, "write longmemeval result: %v\n", err)
				return 1
			}
		}
		fmt.Fprintln(a.stdout, bench.FormatLongMemEval(result))
	}

	if strings.TrimSpace(*locomoFile) != "" {
		result, err := bench.RunLoCoMo(context.Background(), *locomoFile, 0, 10, provider)
		if err != nil {
			fmt.Fprintf(a.stderr, "run locomo benchmark: %v\n", err)
			return 1
		}
		if *outDir != "" {
			if err := bench.WriteLoCoMoResult(filepath.Join(*outDir, "locomo.json"), result); err != nil {
				fmt.Fprintf(a.stderr, "write locomo result: %v\n", err)
				return 1
			}
		}
		fmt.Fprintln(a.stdout, bench.FormatLoCoMo(result))
	}

	if strings.TrimSpace(*membenchDir) != "" {
		result, err := bench.RunMemBench(context.Background(), *membenchDir, nil, "movie", 5, 0, provider)
		if err != nil {
			fmt.Fprintf(a.stderr, "run membench benchmark: %v\n", err)
			return 1
		}
		if *outDir != "" {
			if err := bench.WriteMemBenchResult(filepath.Join(*outDir, "membench.json"), result); err != nil {
				fmt.Fprintf(a.stderr, "write membench result: %v\n", err)
				return 1
			}
		}
		fmt.Fprintln(a.stdout, bench.FormatMemBench(result))
	}

	if *convomemLimit > 0 {
		categories := make([]string, 0, len(bench.ConvoMemCategories))
		for key := range bench.ConvoMemCategories {
			categories = append(categories, key)
		}
		result, err := bench.RunConvoMem(context.Background(), categories, *convomemLimit, 10, *convomemCache, provider)
		if err != nil {
			fmt.Fprintf(a.stderr, "run convomem benchmark: %v\n", err)
			return 1
		}
		if *outDir != "" {
			if err := bench.WriteConvoMemResult(filepath.Join(*outDir, "convomem.json"), result); err != nil {
				fmt.Fprintf(a.stderr, "write convomem result: %v\n", err)
				return 1
			}
		}
		fmt.Fprintln(a.stdout, bench.FormatConvoMem(result))
	}

	return 0
}
