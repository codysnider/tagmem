package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/codysnider/tagmem/internal/vector"
	"github.com/codysnider/tagmem/internal/xdg"
)

func (a *App) runDoctor(paths xdg.Paths, provider vector.Provider) int {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	report := provider.Doctor(ctx)

	fmt.Fprintln(a.stdout, "tagmem doctor")
	fmt.Fprintln(a.stdout, "")
	fmt.Fprintf(a.stdout, "provider:   %s\n", report.Provider)
	fmt.Fprintf(a.stdout, "embed:      %s\n", report.Description)
	fmt.Fprintf(a.stdout, "index:      %s\n", provider.IndexPath(paths.IndexDir))
	fmt.Fprintf(a.stdout, "store:      %s\n", paths.StorePath)
	if report.Model != "" {
		fmt.Fprintf(a.stdout, "model:      %s\n", report.Model)
	}
	if report.ExecutionDevice != "" {
		fmt.Fprintf(a.stdout, "device:     %s\n", report.ExecutionDevice)
	}
	if report.RuntimeLibrary != "" {
		fmt.Fprintf(a.stdout, "runtime:    %s\n", report.RuntimeLibrary)
	}
	if report.BaseURL != "" {
		fmt.Fprintf(a.stdout, "base url:   %s\n", report.BaseURL)
	}
	fmt.Fprintf(a.stdout, "reachable:  %s\n", yesNo(report.Reachable))
	fmt.Fprintf(a.stdout, "embed test: %s\n", yesNo(report.EmbeddingWorks))
	if report.EmbeddingDimensions > 0 {
		fmt.Fprintf(a.stdout, "dimensions: %d\n", report.EmbeddingDimensions)
	}
	if report.Diagnosis != "" {
		fmt.Fprintf(a.stdout, "diagnosis:  %s\n", report.Diagnosis)
	}
	if report.Hint != "" {
		fmt.Fprintf(a.stdout, "hint:       %s\n", report.Hint)
	}
	if report.Error != "" {
		fmt.Fprintf(a.stdout, "error:      %s\n", report.Error)
		return 1
	}

	return 0
}

func yesNo(ok bool) string {
	if ok {
		return "yes"
	}
	return "no"
}
