package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/codysnider/tagmem/internal/vector"
)

type LoCoMoDialog struct {
	DiaID   string `json:"dia_id"`
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

type LoCoMoQA struct {
	Question string   `json:"question"`
	Category int      `json:"category"`
	Evidence []string `json:"evidence"`
}

type LoCoMoResult struct {
	Questions      int                `json:"questions"`
	AverageRecall  float64            `json:"average_recall"`
	ElapsedSeconds float64            `json:"elapsed_seconds"`
	PerCategory    map[int]float64    `json:"per_category"`
	Distribution   map[string]int     `json:"distribution"`
	Results        []LoCoMoItemResult `json:"results,omitempty"`
}

type LoCoMoItemResult struct {
	Question  string   `json:"question"`
	Category  int      `json:"category"`
	Evidence  []string `json:"evidence"`
	Retrieved []string `json:"retrieved"`
	Recall    float64  `json:"recall"`
}

func RunLoCoMo(ctx context.Context, dataFile string, limit int, topK int, provider vector.Provider) (LoCoMoResult, error) {
	data, err := os.ReadFile(dataFile)
	if err != nil {
		return LoCoMoResult{}, err
	}
	var samples []map[string]json.RawMessage
	if err := json.Unmarshal(data, &samples); err != nil {
		return LoCoMoResult{}, err
	}
	if limit > 0 && limit < len(samples) {
		samples = samples[:limit]
	}

	started := time.Now()
	type bucket struct {
		sum   float64
		count int
	}
	perCategory := map[int]bucket{}
	var totalRecall float64
	var totalQuestions int
	results := make([]LoCoMoItemResult, 0)
	totalQACount := 0
	for _, raw := range samples {
		var qaPairs []LoCoMoQA
		if err := json.Unmarshal(raw["qa"], &qaPairs); err == nil {
			totalQACount += len(qaPairs)
		}
	}

	for _, raw := range samples {
		documents, corpusIDs, metadata, qaPairs, err := buildLoCoMoCorpus(raw)
		if err != nil {
			return LoCoMoResult{}, err
		}
		for _, qa := range qaPairs {
			ranked, err := rankDocuments(ctx, provider, documents, metadata, qa.Question, min(max(topK*3, topK), len(documents)))
			if err != nil {
				return LoCoMoResult{}, err
			}
			retrieved := make([]string, 0, min(topK, len(ranked)))
			for i, idx := range ranked {
				if i >= topK {
					break
				}
				retrieved = append(retrieved, corpusIDs[idx])
			}
			recall := computeLoCoMoRecall(retrieved, evidenceToSessionIDs(qa.Evidence))
			totalRecall += recall
			totalQuestions++
			b := perCategory[qa.Category]
			b.sum += recall
			b.count++
			perCategory[qa.Category] = b
			results = append(results, LoCoMoItemResult{Question: qa.Question, Category: qa.Category, Evidence: qa.Evidence, Retrieved: retrieved, Recall: recall})
			if totalQuestions%100 == 0 {
				fmt.Printf("  [LoCoMo] %d/%d questions processed (%.1fs)\n", totalQuestions, totalQACount, time.Since(started).Seconds())
			}
		}
	}

	result := LoCoMoResult{Questions: totalQuestions, ElapsedSeconds: time.Since(started).Seconds(), PerCategory: map[int]float64{}, Distribution: map[string]int{"perfect": 0, "partial": 0, "zero": 0}, Results: results}
	if totalQuestions > 0 {
		result.AverageRecall = totalRecall / float64(totalQuestions)
	}
	for key, bucket := range perCategory {
		if bucket.count == 0 {
			continue
		}
		result.PerCategory[key] = bucket.sum / float64(bucket.count)
	}
	for _, item := range results {
		switch {
		case item.Recall >= 1:
			result.Distribution["perfect"]++
		case item.Recall == 0:
			result.Distribution["zero"]++
		default:
			result.Distribution["partial"]++
		}
	}
	return result, nil
}

func buildLoCoMoCorpus(raw map[string]json.RawMessage) ([]string, []string, []map[string]string, []LoCoMoQA, error) {
	var qaPairs []LoCoMoQA
	if err := json.Unmarshal(raw["qa"], &qaPairs); err != nil {
		return nil, nil, nil, nil, err
	}
	var conversation map[string]json.RawMessage
	if err := json.Unmarshal(raw["conversation"], &conversation); err != nil {
		return nil, nil, nil, nil, err
	}
	documents := []string{}
	ids := []string{}
	metadata := []map[string]string{}
	for session := 1; session <= 32; session++ {
		key := fmt.Sprintf("session_%d", session)
		blob, ok := conversation[key]
		if !ok || len(blob) == 0 || string(blob) == "null" {
			continue
		}
		var dialogs []LoCoMoDialog
		if err := json.Unmarshal(blob, &dialogs); err != nil {
			return nil, nil, nil, nil, err
		}
		if len(dialogs) == 0 {
			continue
		}
		parts := make([]string, 0, len(dialogs))
		for _, dialog := range dialogs {
			if dialog.Text == "" {
				continue
			}
			parts = append(parts, dialog.Speaker+" said, \""+dialog.Text+"\"")
		}
		if len(parts) == 0 {
			continue
		}
		ids = append(ids, fmt.Sprintf("session_%d", session))
		documents = append(documents, strings.Join(parts, "\n"))
		metadata = append(metadata, map[string]string{"corpus_id": fmt.Sprintf("session_%d", session)})
	}
	return documents, ids, metadata, qaPairs, nil
}

func evidenceToSessionIDs(evidence []string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, item := range evidence {
		if !strings.HasPrefix(item, "D") {
			continue
		}
		parts := strings.SplitN(strings.TrimPrefix(item, "D"), ":", 2)
		if len(parts) == 0 {
			continue
		}
		out["session_"+parts[0]] = struct{}{}
	}
	return out
}

func computeLoCoMoRecall(retrieved []string, evidence map[string]struct{}) float64 {
	if len(evidence) == 0 {
		return 1
	}
	hits := 0
	for _, item := range retrieved {
		if _, ok := evidence[item]; ok {
			hits++
		}
	}
	return float64(hits) / float64(len(evidence))
}

func FormatLoCoMo(result LoCoMoResult) string {
	var builder strings.Builder
	builder.WriteString("LoCoMo\n\n")
	builder.WriteString(fmt.Sprintf("Questions:    %d\n", result.Questions))
	builder.WriteString(fmt.Sprintf("Avg Recall:   %.3f\n", result.AverageRecall))
	builder.WriteString(fmt.Sprintf("Time:         %.1fs\n", result.ElapsedSeconds))
	builder.WriteString("\nDistribution:\n")
	builder.WriteString(fmt.Sprintf("  perfect: %d\n", result.Distribution["perfect"]))
	builder.WriteString(fmt.Sprintf("  partial: %d\n", result.Distribution["partial"]))
	builder.WriteString(fmt.Sprintf("  zero:    %d\n", result.Distribution["zero"]))
	if len(result.PerCategory) > 0 {
		builder.WriteString("\nPer category:\n")
		keys := make([]int, 0, len(result.PerCategory))
		for key := range result.PerCategory {
			keys = append(keys, key)
		}
		sort.Ints(keys)
		for _, key := range keys {
			builder.WriteString(fmt.Sprintf("  %d: %.3f\n", key, result.PerCategory[key]))
		}
	}
	return builder.String()
}

func WriteLoCoMoResult(path string, result LoCoMoResult) error {
	return writeJSON(path, result)
}
