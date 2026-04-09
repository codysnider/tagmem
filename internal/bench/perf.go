package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/vector"
)

type PerfResult struct {
	Entries                int          `json:"entries"`
	Searches               int          `json:"searches"`
	StoreSizeBytes         int64        `json:"store_size_bytes"`
	IndexSizeBytes         int64        `json:"index_size_bytes"`
	InitMs                 float64      `json:"init_ms"`
	AddLatency             LatencyStats `json:"add_latency"`
	SearchLatency          LatencyStats `json:"search_latency"`
	AddThroughputPerSec    float64      `json:"add_throughput_per_sec"`
	SearchThroughputPerSec float64      `json:"search_throughput_per_sec"`
}

func RunPerf(root string, entries, searches int, provider vector.Provider) (PerfResult, error) {
	storePath := filepath.Join(root, "store.json")
	indexPath := filepath.Join(root, "vector")
	repo := store.NewRepository(storePath, indexPath, provider)

	started := perfNow()
	if err := repo.Init(); err != nil {
		return PerfResult{}, err
	}
	initMs := time.Since(started).Seconds() * 1000

	addStarted := perfNow()
	addSamples := make([]float64, 0, entries)
	batch := make([]store.AddEntry, 0, 100)
	for i := 0; i < entries; i++ {
		batch = append(batch, store.AddEntry{Depth: i % 3, Title: fmt.Sprintf("Entry %d", i), Body: fmt.Sprintf("Synthetic benchmark document %d about auth billing deploy testing and memory retrieval.", i), Tags: []string{"bench"}})
		if len(batch) < 100 && i+1 < entries {
			continue
		}
		singleStarted := perfNow()
		added, err := repo.AddMany(batch)
		if err != nil {
			return PerfResult{}, err
		}
		elapsedPerEntry := time.Since(singleStarted).Seconds() * 1000 / float64(len(added))
		for range added {
			addSamples = append(addSamples, elapsedPerEntry)
		}
		fmt.Printf("  [Perf] added %d/%d entries\n", len(addSamples), entries)
		batch = batch[:0]
	}
	addElapsed := time.Since(addStarted).Seconds()

	searchStarted := perfNow()
	searchSamples := make([]float64, 0, searches)
	for i := 0; i < searches; i++ {
		singleStarted := perfNow()
		_, err := repo.Search(store.Query{Text: "auth retrieval benchmark", Limit: 5})
		if err != nil {
			return PerfResult{}, err
		}
		searchSamples = append(searchSamples, time.Since(singleStarted).Seconds()*1000)
		if (i+1)%50 == 0 {
			fmt.Printf("  [Perf] completed %d/%d searches\n", i+1, searches)
		}
	}
	searchElapsed := time.Since(searchStarted).Seconds()

	storeInfo, _ := os.Stat(storePath)
	storeSize := int64(0)
	if storeInfo != nil {
		storeSize = storeInfo.Size()
	}
	indexSize, _ := dirSize(indexPath)

	result := PerfResult{
		Entries:        entries,
		Searches:       searches,
		StoreSizeBytes: storeSize,
		IndexSizeBytes: indexSize,
		InitMs:         initMs,
		AddLatency:     computeLatencyStats(addSamples),
		SearchLatency:  computeLatencyStats(searchSamples),
	}
	if addElapsed > 0 {
		result.AddThroughputPerSec = float64(entries) / addElapsed
	}
	if searchElapsed > 0 {
		result.SearchThroughputPerSec = float64(searches) / searchElapsed
	}
	return result, nil
}

func FormatPerf(result PerfResult) string {
	return fmt.Sprintf("Performance\n\nEntries:           %d\nSearches:          %d\nInit:              %.2f ms\nStore size:        %d bytes\nIndex size:        %d bytes\nAdd avg/p95/p99:   %.2f / %.2f / %.2f ms\nAdd throughput:    %.2f ops/sec\nSearch avg/p95/p99 %.2f / %.2f / %.2f ms\nSearch throughput: %.2f ops/sec\n", result.Entries, result.Searches, result.InitMs, result.StoreSizeBytes, result.IndexSizeBytes, result.AddLatency.AvgMs, result.AddLatency.P95Ms, result.AddLatency.P99Ms, result.AddThroughputPerSec, result.SearchLatency.AvgMs, result.SearchLatency.P95Ms, result.SearchLatency.P99Ms, result.SearchThroughputPerSec)
}

func WritePerfResult(path string, result PerfResult) error {
	return writeJSON(path, result)
}

func dirSize(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total, err
}
