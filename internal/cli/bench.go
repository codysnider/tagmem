package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/codysnider/tagmem/internal/bench"
	"github.com/codysnider/tagmem/internal/vector"
)

func benchmarkPaths(value string) (bool, bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "component":
		return true, false, nil
	case "interface":
		return false, true, nil
	case "both":
		return true, true, nil
	default:
		return false, false, fmt.Errorf("invalid benchmark path %q", value)
	}
}

func writeBenchmarkOutput(path string, component any, interfaceResult any) error {
	if path == "" {
		return nil
	}
	if component != nil && interfaceResult != nil {
		return writeJSONFile(path, map[string]any{"component": component, "interface": interfaceResult})
	}
	if component != nil {
		return writeJSONFile(path, component)
	}
	return writeJSONFile(path, interfaceResult)
}

func writeJSONFile(path string, value any) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func suiteOutputPath(root, name string, useInterface bool) string {
	if root == "" {
		return ""
	}
	if useInterface {
		return filepath.Join(root, name+"-interface.json")
	}
	return filepath.Join(root, name+".json")
}

func printBenchmarkResult(stdout any, label string, formatted string) {
	writer, ok := stdout.(interface{ Write([]byte) (int, error) })
	if !ok {
		return
	}
	_, _ = writer.Write([]byte(label + "\n" + formatted + "\n"))
}

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
	path := fs.String("path", "component", "component, interface, or both")
	interfaceCacheDir := fs.String("interface-cache-dir", "", "optional persistent corpus cache dir for interface mode")
	out := fs.String("out", "", "optional JSON output path")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.stderr, "usage: tagmem bench longmemeval [--limit N] <data-file>")
		return 1
	}

	runComponent, runInterface, err := benchmarkPaths(*path)
	if err != nil {
		fmt.Fprintf(a.stderr, "%v\n", err)
		return 1
	}
	var componentResult *bench.LongMemEvalResult
	var interfaceResult *bench.LongMemEvalResult
	if runComponent {
		result, err := bench.RunLongMemEval(context.Background(), fs.Arg(0), *limit, provider)
		if err != nil {
			fmt.Fprintf(a.stderr, "run longmemeval component benchmark: %v\n", err)
			return 1
		}
		componentResult = &result
	}
	if runInterface {
		result, err := bench.RunLongMemEvalInterfaceWithOptions(context.Background(), fs.Arg(0), *limit, provider, bench.LongMemEvalInterfaceOptions{CorpusCacheDir: *interfaceCacheDir})
		if err != nil {
			fmt.Fprintf(a.stderr, "run longmemeval interface benchmark: %v\n", err)
			return 1
		}
		interfaceResult = &result
	}
	if err := writeBenchmarkOutput(*out, componentResult, interfaceResult); err != nil {
		fmt.Fprintf(a.stderr, "write longmemeval result: %v\n", err)
		return 1
	}
	if componentResult != nil {
		printBenchmarkResult(a.stdout, "[component]", bench.FormatLongMemEval(*componentResult))
	}
	if interfaceResult != nil {
		printBenchmarkResult(a.stdout, "[interface]", bench.FormatLongMemEval(*interfaceResult))
	}
	return 0
}

func (a *App) runBenchLoCoMo(args []string, provider vector.Provider) int {
	fs := flag.NewFlagSet("bench locomo", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	limit := fs.Int("limit", 0, "optional number of conversations to run")
	topK := fs.Int("top-k", 10, "retrieval top-k")
	path := fs.String("path", "component", "component, interface, or both")
	out := fs.String("out", "", "optional JSON output path")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(a.stderr, "usage: tagmem bench locomo [--limit N] [--top-k N] <data-file>")
		return 1
	}

	runComponent, runInterface, err := benchmarkPaths(*path)
	if err != nil {
		fmt.Fprintf(a.stderr, "%v\n", err)
		return 1
	}
	var componentResult *bench.LoCoMoResult
	var interfaceResult *bench.LoCoMoResult
	if runComponent {
		result, err := bench.RunLoCoMo(context.Background(), fs.Arg(0), *limit, *topK, provider)
		if err != nil {
			fmt.Fprintf(a.stderr, "run locomo component benchmark: %v\n", err)
			return 1
		}
		componentResult = &result
	}
	if runInterface {
		result, err := bench.RunLoCoMoInterface(context.Background(), fs.Arg(0), *limit, *topK, provider)
		if err != nil {
			fmt.Fprintf(a.stderr, "run locomo interface benchmark: %v\n", err)
			return 1
		}
		interfaceResult = &result
	}
	if err := writeBenchmarkOutput(*out, componentResult, interfaceResult); err != nil {
		fmt.Fprintf(a.stderr, "write locomo result: %v\n", err)
		return 1
	}
	if componentResult != nil {
		printBenchmarkResult(a.stdout, "[component]", bench.FormatLoCoMo(*componentResult))
	}
	if interfaceResult != nil {
		printBenchmarkResult(a.stdout, "[interface]", bench.FormatLoCoMo(*interfaceResult))
	}
	return 0
}

func (a *App) runBenchConvoMem(args []string, provider vector.Provider) int {
	fs := flag.NewFlagSet("bench convomem", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	limit := fs.Int("limit", 10, "items per category")
	topK := fs.Int("top-k", 10, "retrieval top-k")
	cacheDir := fs.String("cache-dir", "/tmp/convomem_cache", "cache directory")
	category := fs.String("category", "all", "category name or 'all'")
	path := fs.String("path", "component", "component, interface, or both")
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
	runComponent, runInterface, err := benchmarkPaths(*path)
	if err != nil {
		fmt.Fprintf(a.stderr, "%v\n", err)
		return 1
	}
	var componentResult *bench.ConvoMemResult
	var interfaceResult *bench.ConvoMemResult
	if runComponent {
		result, err := bench.RunConvoMem(context.Background(), categories, *limit, *topK, *cacheDir, provider)
		if err != nil {
			fmt.Fprintf(a.stderr, "run convomem component benchmark: %v\n", err)
			return 1
		}
		componentResult = &result
	}
	if runInterface {
		result, err := bench.RunConvoMemInterface(context.Background(), categories, *limit, *topK, *cacheDir, provider)
		if err != nil {
			fmt.Fprintf(a.stderr, "run convomem interface benchmark: %v\n", err)
			return 1
		}
		interfaceResult = &result
	}
	if err := writeBenchmarkOutput(*out, componentResult, interfaceResult); err != nil {
		fmt.Fprintf(a.stderr, "write convomem result: %v\n", err)
		return 1
	}
	if componentResult != nil {
		printBenchmarkResult(a.stdout, "[component]", bench.FormatConvoMem(*componentResult))
	}
	if interfaceResult != nil {
		printBenchmarkResult(a.stdout, "[interface]", bench.FormatConvoMem(*interfaceResult))
	}
	return 0
}

func (a *App) runBenchMemBench(args []string, provider vector.Provider) int {
	fs := flag.NewFlagSet("bench membench", flag.ContinueOnError)
	fs.SetOutput(a.stderr)
	category := fs.String("category", "", "single category to run")
	topic := fs.String("topic", "movie", "topic filter")
	topK := fs.Int("top-k", 5, "retrieval top-k")
	limit := fs.Int("limit", 0, "limit items")
	path := fs.String("path", "component", "component, interface, or both")
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
	runComponent, runInterface, err := benchmarkPaths(*path)
	if err != nil {
		fmt.Fprintf(a.stderr, "%v\n", err)
		return 1
	}
	var componentResult *bench.MemBenchResult
	var interfaceResult *bench.MemBenchResult
	if runComponent {
		result, err := bench.RunMemBench(context.Background(), fs.Arg(0), categories, *topic, *topK, *limit, provider)
		if err != nil {
			fmt.Fprintf(a.stderr, "run membench component benchmark: %v\n", err)
			return 1
		}
		componentResult = &result
	}
	if runInterface {
		result, err := bench.RunMemBenchInterface(context.Background(), fs.Arg(0), categories, *topic, *topK, *limit, provider)
		if err != nil {
			fmt.Fprintf(a.stderr, "run membench interface benchmark: %v\n", err)
			return 1
		}
		interfaceResult = &result
	}
	if err := writeBenchmarkOutput(*out, componentResult, interfaceResult); err != nil {
		fmt.Fprintf(a.stderr, "write membench result: %v\n", err)
		return 1
	}
	if componentResult != nil {
		printBenchmarkResult(a.stdout, "[component]", bench.FormatMemBench(*componentResult))
	}
	if interfaceResult != nil {
		printBenchmarkResult(a.stdout, "[interface]", bench.FormatMemBench(*interfaceResult))
	}
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
	path := fs.String("path", "component", "component, interface, or both")
	interfaceCacheDir := fs.String("interface-cache-dir", "", "optional persistent corpus cache dir for LongMemEval interface mode")
	outDir := fs.String("out-dir", "", "optional directory for JSON outputs")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	runComponent, runInterface, err := benchmarkPaths(*path)
	if err != nil {
		fmt.Fprintf(a.stderr, "%v\n", err)
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
		if runComponent {
			result, err := bench.RunLongMemEval(context.Background(), *longFile, 0, provider)
			if err != nil {
				fmt.Fprintf(a.stderr, "run longmemeval component benchmark: %v\n", err)
				return 1
			}
			if err := writeJSONFile(suiteOutputPath(*outDir, "longmemeval", false), result); err != nil {
				fmt.Fprintf(a.stderr, "write longmemeval result: %v\n", err)
				return 1
			}
			printBenchmarkResult(a.stdout, "[component]", bench.FormatLongMemEval(result))
		}
		if runInterface {
			result, err := bench.RunLongMemEvalInterfaceWithOptions(context.Background(), *longFile, 0, provider, bench.LongMemEvalInterfaceOptions{CorpusCacheDir: *interfaceCacheDir})
			if err != nil {
				fmt.Fprintf(a.stderr, "run longmemeval interface benchmark: %v\n", err)
				return 1
			}
			if err := writeJSONFile(suiteOutputPath(*outDir, "longmemeval", true), result); err != nil {
				fmt.Fprintf(a.stderr, "write longmemeval interface result: %v\n", err)
				return 1
			}
			printBenchmarkResult(a.stdout, "[interface]", bench.FormatLongMemEval(result))
		}
	}

	if strings.TrimSpace(*locomoFile) != "" {
		if runComponent {
			result, err := bench.RunLoCoMo(context.Background(), *locomoFile, 0, 10, provider)
			if err != nil {
				fmt.Fprintf(a.stderr, "run locomo component benchmark: %v\n", err)
				return 1
			}
			if err := writeJSONFile(suiteOutputPath(*outDir, "locomo", false), result); err != nil {
				fmt.Fprintf(a.stderr, "write locomo result: %v\n", err)
				return 1
			}
			printBenchmarkResult(a.stdout, "[component]", bench.FormatLoCoMo(result))
		}
		if runInterface {
			result, err := bench.RunLoCoMoInterface(context.Background(), *locomoFile, 0, 10, provider)
			if err != nil {
				fmt.Fprintf(a.stderr, "run locomo interface benchmark: %v\n", err)
				return 1
			}
			if err := writeJSONFile(suiteOutputPath(*outDir, "locomo", true), result); err != nil {
				fmt.Fprintf(a.stderr, "write locomo interface result: %v\n", err)
				return 1
			}
			printBenchmarkResult(a.stdout, "[interface]", bench.FormatLoCoMo(result))
		}
	}

	if strings.TrimSpace(*membenchDir) != "" {
		if runComponent {
			result, err := bench.RunMemBench(context.Background(), *membenchDir, nil, "movie", 5, 0, provider)
			if err != nil {
				fmt.Fprintf(a.stderr, "run membench component benchmark: %v\n", err)
				return 1
			}
			if err := writeJSONFile(suiteOutputPath(*outDir, "membench", false), result); err != nil {
				fmt.Fprintf(a.stderr, "write membench result: %v\n", err)
				return 1
			}
			printBenchmarkResult(a.stdout, "[component]", bench.FormatMemBench(result))
		}
		if runInterface {
			result, err := bench.RunMemBenchInterface(context.Background(), *membenchDir, nil, "movie", 5, 0, provider)
			if err != nil {
				fmt.Fprintf(a.stderr, "run membench interface benchmark: %v\n", err)
				return 1
			}
			if err := writeJSONFile(suiteOutputPath(*outDir, "membench", true), result); err != nil {
				fmt.Fprintf(a.stderr, "write membench interface result: %v\n", err)
				return 1
			}
			printBenchmarkResult(a.stdout, "[interface]", bench.FormatMemBench(result))
		}
	}

	if *convomemLimit > 0 {
		categories := make([]string, 0, len(bench.ConvoMemCategories))
		for key := range bench.ConvoMemCategories {
			categories = append(categories, key)
		}
		if runComponent {
			result, err := bench.RunConvoMem(context.Background(), categories, *convomemLimit, 10, *convomemCache, provider)
			if err != nil {
				fmt.Fprintf(a.stderr, "run convomem component benchmark: %v\n", err)
				return 1
			}
			if err := writeJSONFile(suiteOutputPath(*outDir, "convomem", false), result); err != nil {
				fmt.Fprintf(a.stderr, "write convomem result: %v\n", err)
				return 1
			}
			printBenchmarkResult(a.stdout, "[component]", bench.FormatConvoMem(result))
		}
		if runInterface {
			result, err := bench.RunConvoMemInterface(context.Background(), categories, *convomemLimit, 10, *convomemCache, provider)
			if err != nil {
				fmt.Fprintf(a.stderr, "run convomem interface benchmark: %v\n", err)
				return 1
			}
			if err := writeJSONFile(suiteOutputPath(*outDir, "convomem", true), result); err != nil {
				fmt.Fprintf(a.stderr, "write convomem interface result: %v\n", err)
				return 1
			}
			printBenchmarkResult(a.stdout, "[interface]", bench.FormatConvoMem(result))
		}
	}

	return 0
}
