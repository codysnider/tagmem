package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/codysnider/tagmem/internal/retrieval"
	"github.com/codysnider/tagmem/internal/vector"
)

var memBenchFiles = map[string]string{
	"simple":           "simple.json",
	"highlevel":        "highlevel.json",
	"knowledge_update": "knowledge_update.json",
	"comparative":      "comparative.json",
	"conditional":      "conditional.json",
	"noisy":            "noisy.json",
	"aggregative":      "aggregative.json",
	"highlevel_rec":    "highlevel_rec.json",
	"lowlevel_rec":     "lowlevel_rec.json",
	"RecMultiSession":  "RecMultiSession.json",
	"post_processing":  "post_processing.json",
}

type MemBenchItem struct {
	Category      string          `json:"category"`
	Topic         string          `json:"topic"`
	TID           int             `json:"tid"`
	Turns         interface{}     `json:"turns"`
	Question      string          `json:"question"`
	GroundTruth   string          `json:"ground_truth"`
	AnswerText    string          `json:"answer_text"`
	TargetStepIDs [][]interface{} `json:"target_step_ids"`
}

type MemBenchResult struct {
	Items          int                  `json:"items"`
	RecallAtK      float64              `json:"recall_at_k"`
	TopK           int                  `json:"top_k"`
	ElapsedSeconds float64              `json:"elapsed_seconds"`
	PerCategory    map[string]float64   `json:"per_category"`
	Results        []MemBenchItemResult `json:"results,omitempty"`
}

type MemBenchItemResult struct {
	Category       string `json:"category"`
	Topic          string `json:"topic"`
	Question       string `json:"question"`
	GroundTruth    string `json:"ground_truth"`
	TargetSteps    []int  `json:"target_steps"`
	RetrievedSteps []int  `json:"retrieved_steps"`
	HitAtK         bool   `json:"hit_at_k"`
}

func RunMemBench(ctx context.Context, dataDir string, categories []string, topic string, topK, limit int, provider vector.Provider) (MemBenchResult, error) {
	items, err := loadMemBench(dataDir, categories, topic, limit)
	if err != nil {
		return MemBenchResult{}, err
	}
	type bucket struct {
		hit   int
		total int
	}
	perCategory := map[string]bucket{}
	results := make([]MemBenchItemResult, 0, len(items))
	hits := 0
	started := time.Now()

	for index, item := range items {
		documents, metas, stepIDs := memBenchTurns(item.Turns)
		if len(documents) == 0 {
			continue
		}
		nRetrieve := min(max(topK*3, topK), len(documents))
		ranked, err := rankDocuments(ctx, provider, documents, metas, item.Question, nRetrieve)
		if err != nil {
			return MemBenchResult{}, err
		}
		predicateKeywords := predicateKeywords(item.Question)
		type scored struct {
			idx   int
			score float64
		}
		scoredDocs := make([]scored, 0, nRetrieve)
		for i, docIndex := range ranked {
			if i >= nRetrieve {
				break
			}
			overlap := retrieval.KeywordOverlap(predicateKeywords, documents[docIndex])
			scoredDocs = append(scoredDocs, scored{idx: docIndex, score: overlap})
		}
		sort.SliceStable(scoredDocs, func(i, j int) bool { return scoredDocs[i].score > scoredDocs[j].score })
		retrieved := make([]int, 0, min(topK, len(scoredDocs)))
		for i, itemScore := range scoredDocs {
			if i >= topK {
				break
			}
			retrieved = append(retrieved, stepIDs[itemScore.idx])
		}
		targets := memBenchTargets(item.TargetStepIDs)
		hit := intersects(targets, retrieved)
		if hit {
			hits++
		}
		bucket := perCategory[item.Category]
		bucket.total++
		if hit {
			bucket.hit++
		}
		perCategory[item.Category] = bucket
		results = append(results, MemBenchItemResult{Category: item.Category, Topic: item.Topic, Question: item.Question, GroundTruth: item.GroundTruth, TargetSteps: targets, RetrievedSteps: retrieved, HitAtK: hit})
		if (index+1)%100 == 0 {
			fmt.Printf("  [MemBench] %d/%d items processed (%.1fs)\n", index+1, len(items), time.Since(started).Seconds())
		}
	}

	result := MemBenchResult{Items: len(results), TopK: topK, ElapsedSeconds: time.Since(started).Seconds(), PerCategory: map[string]float64{}, Results: results}
	if len(results) > 0 {
		result.RecallAtK = float64(hits) / float64(len(results))
	}
	for key, bucket := range perCategory {
		if bucket.total == 0 {
			continue
		}
		result.PerCategory[key] = float64(bucket.hit) / float64(bucket.total)
	}
	return result, nil
}

func loadMemBench(dataDir string, categories []string, topic string, limit int) ([]MemBenchItem, error) {
	if len(categories) == 0 {
		for category := range memBenchFiles {
			categories = append(categories, category)
		}
		sort.Strings(categories)
	}
	items := make([]MemBenchItem, 0)
	for _, category := range categories {
		filename, ok := memBenchFiles[category]
		if !ok {
			continue
		}
		path := filepath.Join(dataDir, filename)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var raw map[string][]map[string]interface{}
		if err := json.Unmarshal(data, &raw); err != nil {
			return nil, err
		}
		for key, list := range raw {
			if topic != "" && key != topic && key != "roles" && key != "events" {
				continue
			}
			for _, item := range list {
				qa, _ := item["QA"].(map[string]interface{})
				turns := item["message_list"]
				if qa == nil || turns == nil {
					continue
				}
				result := MemBenchItem{Category: category, Topic: key, TID: toInt(item["tid"]), Turns: turns, Question: toString(qa["question"]), GroundTruth: toString(qa["ground_truth"]), AnswerText: toString(qa["answer"]), TargetStepIDs: toStepIDs(qa["target_step_id"])}
				if result.Question == "" {
					continue
				}
				items = append(items, result)
				if limit > 0 && len(items) >= limit {
					return items, nil
				}
			}
		}
	}
	return items, nil
}

func memBenchTurns(raw interface{}) ([]string, []map[string]string, []int) {
	documents := []string{}
	metadata := []map[string]string{}
	steps := []int{}
	global := 0
	appendTurn := func(turn map[string]interface{}) {
		user := toString(turn["user"])
		if user == "" {
			user = toString(turn["user_message"])
		}
		assistant := toString(turn["assistant"])
		if assistant == "" {
			assistant = toString(turn["assistant_message"])
		}
		timeText := toString(turn["time"])
		text := "[User] " + user + " [Assistant] " + assistant
		if timeText != "" {
			text = "[" + timeText + "] " + text
		}
		documents = append(documents, text)
		sid := toInt(turn["sid"])
		if sid == 0 {
			sid = toInt(turn["mid"])
		}
		if sid == 0 {
			sid = global
		}
		steps = append(steps, sid)
		metadata = append(metadata, map[string]string{"sid": fmt.Sprintf("%d", sid), "global_idx": fmt.Sprintf("%d", global)})
		global++
	}
	switch v := raw.(type) {
	case []interface{}:
		if len(v) > 0 {
			if _, ok := v[0].(map[string]interface{}); ok {
				for _, item := range v {
					turn, _ := item.(map[string]interface{})
					if turn != nil {
						appendTurn(turn)
					}
				}
			} else {
				for _, session := range v {
					sessionTurns, _ := session.([]interface{})
					for _, item := range sessionTurns {
						turn, _ := item.(map[string]interface{})
						if turn != nil {
							appendTurn(turn)
						}
					}
				}
			}
		}
	}
	return documents, metadata, steps
}

func memBenchTargets(raw [][]interface{}) []int {
	values := []int{}
	for _, step := range raw {
		if len(step) < 1 {
			continue
		}
		values = append(values, toInt(step[0]))
	}
	return values
}

func predicateKeywords(question string) []string {
	names := map[string]struct{}{}
	for _, token := range strings.Fields(question) {
		if len(token) > 2 && token[0] >= 'A' && token[0] <= 'Z' {
			names[strings.ToLower(strings.Trim(token, ",.?!:;"))] = struct{}{}
		}
	}
	keywords := retrieval.ExtractKeywords(question)
	out := make([]string, 0, len(keywords))
	for _, keyword := range keywords {
		if _, ok := names[keyword]; ok {
			continue
		}
		out = append(out, keyword)
	}
	return out
}

func intersects(targets, retrieved []int) bool {
	set := map[int]struct{}{}
	for _, target := range targets {
		set[target] = struct{}{}
	}
	for _, item := range retrieved {
		if _, ok := set[item]; ok {
			return true
		}
	}
	return false
}

func toInt(value interface{}) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		var out int
		fmt.Sscanf(v, "%d", &out)
		return out
	default:
		return 0
	}
}

func toString(value interface{}) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func toStepIDs(value interface{}) [][]interface{} {
	if raw, ok := value.([]interface{}); ok {
		out := make([][]interface{}, 0, len(raw))
		for _, item := range raw {
			if pair, ok := item.([]interface{}); ok {
				out = append(out, pair)
			}
		}
		return out
	}
	return nil
}

func FormatMemBench(result MemBenchResult) string {
	var b strings.Builder
	b.WriteString("MemBench\n\n")
	b.WriteString(fmt.Sprintf("Items:        %d\n", result.Items))
	b.WriteString(fmt.Sprintf("Recall@%d:    %.3f\n", result.TopK, result.RecallAtK))
	b.WriteString(fmt.Sprintf("Time:         %.1fs\n", result.ElapsedSeconds))
	if len(result.PerCategory) > 0 {
		b.WriteString("\nPer category:\n")
		keys := make([]string, 0, len(result.PerCategory))
		for key := range result.PerCategory {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			b.WriteString(fmt.Sprintf("  %s: %.3f\n", key, result.PerCategory[key]))
		}
	}
	return b.String()
}

func WriteMemBenchResult(path string, result MemBenchResult) error { return writeJSON(path, result) }
