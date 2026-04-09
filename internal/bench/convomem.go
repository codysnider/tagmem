package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/codysnider/tagmem/internal/vector"
)

const convoMemHFBase = "https://huggingface.co/datasets/Salesforce/ConvoMem/resolve/main/core_benchmark/evidence_questions"
const convoMemHFAPI = "https://huggingface.co/api/datasets/Salesforce/ConvoMem/tree/main/core_benchmark/evidence_questions"

var ConvoMemCategories = map[string]string{
	"user_evidence":                "User Facts",
	"assistant_facts_evidence":     "Assistant Facts",
	"changing_evidence":            "Changing Facts",
	"abstention_evidence":          "Abstention",
	"preference_evidence":          "Preferences",
	"implicit_connection_evidence": "Implicit Connections",
}

type convoMemTreeEntry struct {
	Path string `json:"path"`
}

type ConvoMemEvidenceFile struct {
	EvidenceItems []ConvoMemItem `json:"evidence_items"`
}

type ConvoMemItem struct {
	Question         string                    `json:"question"`
	Answer           string                    `json:"answer"`
	Conversations    []ConvoMemConversation    `json:"conversations"`
	MessageEvidences []ConvoMemMessageEvidence `json:"message_evidences"`
	CategoryKey      string                    `json:"-"`
}

type ConvoMemConversation struct {
	Messages []ConvoMemMessage `json:"messages"`
}

type ConvoMemMessage struct {
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

type ConvoMemMessageEvidence struct {
	Text string `json:"text"`
}

type ConvoMemItemResult struct {
	Question       string  `json:"question"`
	Category       string  `json:"category"`
	Recall         float64 `json:"recall"`
	RetrievedCount int     `json:"retrieved_count"`
	EvidenceCount  int     `json:"evidence_count"`
	Found          int     `json:"found"`
}

type ConvoMemResult struct {
	Items          int                  `json:"items"`
	AverageRecall  float64              `json:"average_recall"`
	ElapsedSeconds float64              `json:"elapsed_seconds"`
	PerCategory    map[string]float64   `json:"per_category"`
	Distribution   map[string]int       `json:"distribution"`
	Results        []ConvoMemItemResult `json:"results,omitempty"`
}

func RunConvoMem(ctx context.Context, categories []string, limitPerCategory, topK int, cacheDir string, provider vector.Provider) (ConvoMemResult, error) {
	items, err := loadConvoMemItems(categories, limitPerCategory, cacheDir)
	if err != nil {
		return ConvoMemResult{}, err
	}
	type bucket struct {
		sum   float64
		count int
	}
	perCategory := map[string]bucket{}
	results := make([]ConvoMemItemResult, 0, len(items))
	var totalRecall float64
	started := time.Now()

	for _, item := range items {
		recall, detail, err := retrieveConvoMemItem(ctx, item, topK, provider)
		if err != nil {
			return ConvoMemResult{}, err
		}
		totalRecall += recall
		bucket := perCategory[item.CategoryKey]
		bucket.sum += recall
		bucket.count++
		perCategory[item.CategoryKey] = bucket
		results = append(results, ConvoMemItemResult{Question: item.Question, Category: item.CategoryKey, Recall: recall, RetrievedCount: detail.RetrievedCount, EvidenceCount: detail.EvidenceCount, Found: detail.Found})
		if len(results)%10 == 0 {
			fmt.Printf("  [ConvoMem] %d/%d items processed (%.1fs)\n", len(results), len(items), time.Since(started).Seconds())
		}
	}

	result := ConvoMemResult{Items: len(results), ElapsedSeconds: time.Since(started).Seconds(), PerCategory: map[string]float64{}, Distribution: map[string]int{"perfect": 0, "partial": 0, "zero": 0}, Results: results}
	if len(results) > 0 {
		result.AverageRecall = totalRecall / float64(len(results))
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

type convoMemDetail struct {
	RetrievedCount int
	EvidenceCount  int
	Found          int
}

func retrieveConvoMemItem(ctx context.Context, item ConvoMemItem, topK int, provider vector.Provider) (float64, convoMemDetail, error) {
	documents := make([]string, 0)
	metadata := make([]map[string]string, 0)
	for conversationIndex, conversation := range item.Conversations {
		for messageIndex, message := range conversation.Messages {
			text := strings.TrimSpace(message.Text)
			if text == "" {
				continue
			}
			documents = append(documents, text)
			metadata = append(metadata, map[string]string{"corpus_id": fmt.Sprintf("c%d_m%d", conversationIndex, messageIndex), "speaker": message.Speaker})
		}
	}
	if len(documents) == 0 {
		return 0, convoMemDetail{}, nil
	}
	ranked, err := rankDocuments(ctx, provider, documents, metadata, item.Question, min(topK, len(documents)))
	if err != nil {
		return 0, convoMemDetail{}, err
	}
	retrievedTexts := make([]string, 0, min(topK, len(ranked)))
	for i, idx := range ranked {
		if i >= topK {
			break
		}
		retrievedTexts = append(retrievedTexts, strings.ToLower(strings.TrimSpace(documents[idx])))
	}
	evidenceTexts := map[string]struct{}{}
	for _, evidence := range item.MessageEvidences {
		text := strings.ToLower(strings.TrimSpace(evidence.Text))
		if text == "" {
			continue
		}
		evidenceTexts[text] = struct{}{}
	}
	found := 0
	for evidence := range evidenceTexts {
		for _, retrieved := range retrievedTexts {
			if strings.Contains(retrieved, evidence) || strings.Contains(evidence, retrieved) {
				found++
				break
			}
		}
	}
	recall := 1.0
	if len(evidenceTexts) > 0 {
		recall = float64(found) / float64(len(evidenceTexts))
	}
	return recall, convoMemDetail{RetrievedCount: len(retrievedTexts), EvidenceCount: len(evidenceTexts), Found: found}, nil
}

func loadConvoMemItems(categories []string, limitPerCategory int, cacheDir string) ([]ConvoMemItem, error) {
	items := make([]ConvoMemItem, 0)
	for _, category := range categories {
		files, err := discoverConvoMemFiles(category, cacheDir)
		if err != nil {
			return nil, err
		}
		loaded := 0
		for _, file := range files {
			if loaded >= limitPerCategory {
				break
			}
			payload, err := downloadConvoMemFile(category, file, cacheDir)
			if err != nil {
				continue
			}
			for _, item := range payload.EvidenceItems {
				item.CategoryKey = category
				items = append(items, item)
				loaded++
				if loaded >= limitPerCategory {
					break
				}
			}
		}
	}
	return items, nil
}

func discoverConvoMemFiles(category, cacheDir string) ([]string, error) {
	cachePath := filepath.Join(cacheDir, category+"_files.json")
	if data, err := os.ReadFile(cachePath); err == nil {
		var files []string
		if json.Unmarshal(data, &files) == nil && len(files) > 0 {
			return files, nil
		}
	}
	response, err := http.Get(convoMemHFAPI + "/" + category + "/1_evidence?recursive=true")
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	var entries []convoMemTreeEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		var wrapped struct {
			Siblings []convoMemTreeEntry `json:"siblings"`
		}
		if err := json.Unmarshal(body, &wrapped); err != nil {
			return nil, err
		}
		entries = wrapped.Siblings
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Path, ".json") {
			parts := strings.SplitN(entry.Path, category+"/", 2)
			if len(parts) == 2 {
				files = append(files, parts[1])
			}
		}
	}
	sort.Strings(files)
	if err := os.MkdirAll(cacheDir, 0o755); err == nil {
		if data, err := json.Marshal(files); err == nil {
			_ = os.WriteFile(cachePath, data, 0o644)
		}
	}
	return files, nil
}

func downloadConvoMemFile(category, subpath, cacheDir string) (ConvoMemEvidenceFile, error) {
	cachePath := filepath.Join(cacheDir, category, strings.ReplaceAll(subpath, "/", "_"))
	if data, err := os.ReadFile(cachePath); err == nil {
		var payload ConvoMemEvidenceFile
		if json.Unmarshal(data, &payload) == nil {
			return payload, nil
		}
	}
	response, err := http.Get(convoMemHFBase + "/" + category + "/" + subpath)
	if err != nil {
		return ConvoMemEvidenceFile{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return ConvoMemEvidenceFile{}, err
	}
	var payload ConvoMemEvidenceFile
	if err := json.Unmarshal(body, &payload); err != nil {
		return ConvoMemEvidenceFile{}, err
	}
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o755); err == nil {
		_ = os.WriteFile(cachePath, body, 0o644)
	}
	return payload, nil
}

func FormatConvoMem(result ConvoMemResult) string {
	var builder strings.Builder
	builder.WriteString("ConvoMem\n\n")
	builder.WriteString(fmt.Sprintf("Items:        %d\n", result.Items))
	builder.WriteString(fmt.Sprintf("Avg Recall:   %.3f\n", result.AverageRecall))
	builder.WriteString(fmt.Sprintf("Time:         %.1fs\n", result.ElapsedSeconds))
	builder.WriteString("\nDistribution:\n")
	builder.WriteString(fmt.Sprintf("  perfect: %d\n", result.Distribution["perfect"]))
	builder.WriteString(fmt.Sprintf("  partial: %d\n", result.Distribution["partial"]))
	builder.WriteString(fmt.Sprintf("  zero:    %d\n", result.Distribution["zero"]))
	if len(result.PerCategory) > 0 {
		builder.WriteString("\nPer category:\n")
		keys := make([]string, 0, len(result.PerCategory))
		for key := range result.PerCategory {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			builder.WriteString(fmt.Sprintf("  %s: %.3f\n", key, result.PerCategory[key]))
		}
	}
	return builder.String()
}

func WriteConvoMemResult(path string, result ConvoMemResult) error {
	return writeJSON(path, result)
}
