package bench

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	chromem "github.com/philippgille/chromem-go"

	"github.com/codysnider/tagmem/internal/retrieval"
	"github.com/codysnider/tagmem/internal/vector"
)

type rankedDocument struct {
	Index    int
	Distance float64
}

const chunkSize = 250
const chunkOverlap = 40
const addBatchSize = 1

func rankDocuments(ctx context.Context, provider vector.Provider, documents []string, metadata []map[string]string, query string, nResults int) ([]int, error) {
	db := chromem.NewDB()
	collection, err := db.CreateCollection("bench", nil, nil)
	if err != nil {
		return nil, err
	}

	chunkedDocs, chunkedMeta := chunkDocuments(documents, metadata)
	ids := make([]string, 0, len(chunkedDocs))
	for i := range chunkedDocs {
		ids = append(ids, benchDocID(i))
	}
	for start := 0; start < len(chunkedDocs); start += addBatchSize {
		end := min(start+addBatchSize, len(chunkedDocs))
		for i := start; i < end; i++ {
			embedding, err := embedWithRetry(ctx, provider, chunkedDocs[i])
			if err != nil {
				return nil, err
			}
			if err := collection.Add(ctx, []string{ids[i]}, [][]float32{embedding}, []map[string]string{chunkedMeta[i]}, []string{chunkedDocs[i]}); err != nil {
				return nil, err
			}
		}
	}

	queryKeywords := retrieval.ExtractKeywords(query)
	if len(queryKeywords) == 0 {
		queryKeywords = []string{query}
	}
	candidateResults := nResults * 4
	if candidateResults < nResults {
		candidateResults = nResults
	}
	if candidateResults > len(chunkedDocs) {
		candidateResults = len(chunkedDocs)
	}
	queryEmbedding, err := embedWithRetry(ctx, provider, query)
	if err != nil {
		return nil, err
	}
	results, err := collection.QueryEmbedding(ctx, queryEmbedding, candidateResults, nil, nil)
	if err != nil {
		return nil, err
	}

	bestByIndex := map[int]rankedDocument{}
	for _, result := range results {
		index := parseChunkParent(result.Metadata)
		if index < 0 || index >= len(documents) {
			continue
		}
		overlap := retrieval.KeywordOverlap(queryKeywords, documents[index])
		scored := rankedDocument{Index: index, Distance: retrieval.FuseSimilarity(result.Similarity, overlap)}
		current, ok := bestByIndex[index]
		if !ok || scored.Distance < current.Distance {
			bestByIndex[index] = scored
		}
	}

	scored := make([]rankedDocument, 0, len(bestByIndex))
	for _, item := range bestByIndex {
		scored = append(scored, item)
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].Distance == scored[j].Distance {
			return scored[i].Index < scored[j].Index
		}
		return scored[i].Distance < scored[j].Distance
	})

	ranked := make([]int, 0, len(documents))
	seen := map[int]struct{}{}
	for _, item := range scored {
		ranked = append(ranked, item.Index)
		seen[item.Index] = struct{}{}
	}
	for i := range documents {
		if _, ok := seen[i]; ok {
			continue
		}
		ranked = append(ranked, i)
	}
	return ranked, nil
}

func perfNow() time.Time { return time.Now() }

func benchDocID(index int) string { return "doc_" + strconv.Itoa(index) }

func parseBenchDocID(id string) int {
	if len(id) <= 4 {
		return -1
	}
	value, err := strconv.Atoi(id[4:])
	if err != nil {
		return -1
	}
	return value
}

func chunkDocuments(documents []string, metadata []map[string]string) ([]string, []map[string]string) {
	chunkedDocs := make([]string, 0, len(documents))
	chunkedMeta := make([]map[string]string, 0, len(documents))
	for index, document := range documents {
		chunks := splitDocument(document)
		for chunkIndex, chunk := range chunks {
			meta := map[string]string{"parent_index": strconv.Itoa(index), "chunk_index": strconv.Itoa(chunkIndex)}
			for key, value := range metadata[index] {
				meta[key] = value
			}
			chunkedDocs = append(chunkedDocs, chunk)
			chunkedMeta = append(chunkedMeta, meta)
		}
	}
	return chunkedDocs, chunkedMeta
}

func splitDocument(document string) []string {
	if len(document) <= chunkSize {
		return []string{document}
	}
	chunks := []string{}
	for start := 0; start < len(document); {
		end := min(start+chunkSize, len(document))
		if end < len(document) {
			if split := strings.LastIndex(document[start:end], "\n"); split > chunkSize/2 {
				end = start + split
			}
		}
		chunk := strings.TrimSpace(document[start:end])
		if chunk != "" {
			chunks = append(chunks, chunk)
		}
		if end >= len(document) {
			break
		}
		start = max(end-chunkOverlap, start+1)
	}
	if len(chunks) == 0 {
		return []string{document}
	}
	return chunks
}

func parseChunkParent(metadata map[string]string) int {
	if metadata == nil {
		return -1
	}
	value, err := strconv.Atoi(metadata["parent_index"])
	if err != nil {
		return -1
	}
	return value
}

func embedWithRetry(ctx context.Context, provider vector.Provider, text string) ([]float32, error) {
	current := strings.TrimSpace(text)
	for attempts := 0; attempts < 6; attempts++ {
		embedding, err := provider.Func(ctx, current)
		if err == nil {
			return embedding, nil
		}
		errText := strings.ToLower(err.Error())
		if !strings.Contains(errText, "maximum context length") && !strings.Contains(errText, "400 bad request") {
			return nil, err
		}
		if len(current) < 40 {
			return nil, err
		}
		current = strings.TrimSpace(current[:len(current)*3/4])
	}
	return nil, fmt.Errorf("embedding failed after retries")
}
