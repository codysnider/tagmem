package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codysnider/tagmem/internal/retrieval"
	"github.com/codysnider/tagmem/internal/vector"
)

type LongMemEvalEntry struct {
	Question           string   `json:"question"`
	QuestionType       string   `json:"question_type"`
	QuestionDateRaw    string   `json:"question_date"`
	AnswerSessionIDs   []string `json:"answer_session_ids"`
	HaystackSessions   [][]Turn `json:"haystack_sessions"`
	HaystackSessionIDs []string `json:"haystack_session_ids"`
	HaystackDates      []string `json:"haystack_dates"`
}

type Turn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type LongMemEvalResult struct {
	Questions       int                     `json:"questions"`
	RecallAt1       float64                 `json:"recall_at_1"`
	RecallAt5       float64                 `json:"recall_at_5"`
	RecallAt10      float64                 `json:"recall_at_10"`
	MRR             float64                 `json:"mrr"`
	NDCGAt10        float64                 `json:"ndcg_at_10"`
	ElapsedSeconds  float64                 `json:"elapsed_seconds"`
	PerQuestionType map[string]float64      `json:"per_question_type"`
	Distribution    map[string]int          `json:"distribution"`
	Items           []LongMemEvalItemResult `json:"items,omitempty"`
}

type LongMemEvalItemResult struct {
	Question          string   `json:"question"`
	QuestionType      string   `json:"question_type"`
	CorrectSessionIDs []string `json:"correct_session_ids"`
	TopResults        []string `json:"top_results"`
	RecallAt1         float64  `json:"recall_at_1"`
	RecallAt5         float64  `json:"recall_at_5"`
	RecallAt10        float64  `json:"recall_at_10"`
	ReciprocalRank    float64  `json:"reciprocal_rank"`
	NDCGAt10          float64  `json:"ndcg_at_10"`
}

type lmeSessionData struct {
	SessionID      string
	Timestamp      string
	UserChunks     []string
	UserEmbeddings [][]float32
	FullChunks     []string
	FullEmbeddings [][]float32
	PrefChunks     []string
	PrefEmbeddings [][]float32
}

func RunLongMemEval(ctx context.Context, dataFile string, limit int, provider vector.Provider) (LongMemEvalResult, error) {
	data, err := os.ReadFile(dataFile)
	if err != nil {
		return LongMemEvalResult{}, err
	}

	var entries []LongMemEvalEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return LongMemEvalResult{}, err
	}
	if limit > 0 && limit < len(entries) {
		entries = entries[:limit]
	}

	started := time.Now()
	items := make([]LongMemEvalItemResult, len(entries))
	cache := newLMEEmbeddingCache()
	var precomputed map[string]*lmeSessionData
	if len(entries) >= 200 && os.Getenv("TAGMEM_LME_PRECOMPUTE") == "1" {
		precomputeStarted := time.Now()
		fmt.Printf("  [LongMemEval] precomputing reusable session embeddings...\n")
		precomputed, err = precomputeLongMemEvalSessions(ctx, provider, entries, cache)
		if err != nil {
			return LongMemEvalResult{}, err
		}
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		fmt.Printf("  [LongMemEval] precompute done in %.1fs  sessions=%d  heap=%.1fMB  gc=%d\n", time.Since(precomputeStarted).Seconds(), len(precomputed), float64(mem.HeapAlloc)/1024/1024, mem.NumGC)
	}
	questionEmbeddings := map[int][]float32{}
	questionTexts := make([]string, 0, len(entries))
	questionIndexes := make([]int, 0, len(entries))
	for i, entry := range entries {
		questionTexts = append(questionTexts, entry.Question)
		questionIndexes = append(questionIndexes, i)
	}
	if embeddings, err := embedMany(ctx, provider, questionTexts, nil); err == nil {
		for i, index := range questionIndexes {
			questionEmbeddings[index] = embeddings[i]
		}
	}
	workerCount := 1
	if runtime.NumCPU() >= 8 {
		workerCount = 2
	}
	type lmeJobResult struct {
		index int
		item  LongMemEvalItemResult
		err   error
	}
	jobs := make(chan int)
	results := make(chan lmeJobResult, len(entries))
	var processed atomic.Int64
	var wg sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				entry := entries[index]
				rankedIDs, err := rankLongMemEvalEntry(ctx, provider, entry, cache, questionEmbeddings[index], precomputed)
				if err != nil {
					results <- lmeJobResult{index: index, err: err}
					continue
				}
				correct := sliceToSet(entry.AnswerSessionIDs)
				r1 := recallAnyIDsAt(rankedIDs, correct, 1)
				r5 := recallAnyIDsAt(rankedIDs, correct, 5)
				r10 := recallAnyIDsAt(rankedIDs, correct, 10)
				rr := reciprocalRankIDs(rankedIDs, correct)
				n10 := ndcgIDs(rankedIDs, correct, 10)
				topResults := make([]string, 0, min(10, len(rankedIDs)))
				for i, id := range rankedIDs {
					if i >= 10 {
						break
					}
					topResults = append(topResults, id)
				}
				results <- lmeJobResult{index: index, item: LongMemEvalItemResult{Question: entry.Question, QuestionType: entry.QuestionType, CorrectSessionIDs: entry.AnswerSessionIDs, TopResults: topResults, RecallAt1: r1, RecallAt5: r5, RecallAt10: r10, ReciprocalRank: rr, NDCGAt10: n10}}
				count := processed.Add(1)
				if count%25 == 0 {
					var mem runtime.MemStats
					runtime.ReadMemStats(&mem)
					fmt.Printf("  [LongMemEval] %d/%d questions processed (%.1fs) heap=%.1fMB gc=%d cache=%d\n", count, len(entries), time.Since(started).Seconds(), float64(mem.HeapAlloc)/1024/1024, mem.NumGC, cache.size())
				}
			}
		}()
	}
	go func() {
		for index := range entries {
			jobs <- index
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	for result := range results {
		if result.err != nil {
			return LongMemEvalResult{}, result.err
		}
		items[result.index] = result.item
	}
	type bucket struct {
		sum   float64
		count int
	}
	perType := map[string]bucket{}
	var totalAt1, totalAt5, totalAt10, totalMRR, totalNDCG float64
	finalItems := make([]LongMemEvalItemResult, 0, len(items))
	for _, item := range items {
		if item.Question == "" {
			continue
		}
		finalItems = append(finalItems, item)
		totalAt1 += item.RecallAt1
		totalAt5 += item.RecallAt5
		totalAt10 += item.RecallAt10
		totalMRR += item.ReciprocalRank
		totalNDCG += item.NDCGAt10
		b := perType[item.QuestionType]
		b.sum += item.RecallAt5
		b.count++
		perType[item.QuestionType] = b
	}

	result := LongMemEvalResult{
		Questions:       len(entries),
		ElapsedSeconds:  time.Since(started).Seconds(),
		PerQuestionType: map[string]float64{},
		Distribution:    map[string]int{"hit@1": 0, "hit@5": 0, "miss@5": 0},
		Items:           finalItems,
	}
	if len(finalItems) > 0 {
		result.RecallAt1 = totalAt1 / float64(len(finalItems))
		result.RecallAt5 = totalAt5 / float64(len(finalItems))
		result.RecallAt10 = totalAt10 / float64(len(finalItems))
		result.MRR = totalMRR / float64(len(finalItems))
		result.NDCGAt10 = totalNDCG / float64(len(finalItems))
	}
	for _, item := range finalItems {
		if item.RecallAt1 > 0 {
			result.Distribution["hit@1"]++
		}
		if item.RecallAt5 > 0 {
			result.Distribution["hit@5"]++
		} else {
			result.Distribution["miss@5"]++
		}
	}
	keys := make([]string, 0, len(perType))
	for key := range perType {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		bucket := perType[key]
		if bucket.count == 0 {
			continue
		}
		result.PerQuestionType[key] = bucket.sum / float64(bucket.count)
	}
	return result, nil
}

type lmeCandidate struct {
	SessionIndex int
	SessionID    string
	Timestamp    string
	LowerText    string
	Similarity   float64
	IsPreference bool
}

func rankLongMemEvalEntry(ctx context.Context, provider vector.Provider, entry LongMemEvalEntry, cache *lmeEmbeddingCache, queryEmbedding []float32, precomputed map[string]*lmeSessionData) ([]string, error) {
	question := entry.Question
	questionKeywords := retrieval.ExtractKeywords(question)
	quotedPhrases := extractQuotedPhrases(question)
	personNames := extractPersonNames(question)
	questionDate := parseLMEQuestionDate(entry.QuestionDate())
	timeOffsetDays, tolerance, hasTimeOffset := parseTimeOffsetDays(question)
	targetDate := time.Time{}
	if hasTimeOffset && !questionDate.IsZero() {
		targetDate = questionDate.AddDate(0, 0, -timeOffsetDays)
	}

	sessionIDs := make([]string, 0, len(entry.HaystackSessions))
	timestamps := make([]string, 0, len(entry.HaystackSessions))
	for i := range entry.HaystackSessions {
		sessionIDs = append(sessionIDs, entry.HaystackSessionIDs[i])
		timestamps = append(timestamps, entry.HaystackDates[i])
	}
	candidates := make([]lmeCandidate, 0)
	if precomputed != nil {
		filteredIDs := make([]string, 0, len(sessionIDs))
		filteredTS := make([]string, 0, len(sessionIDs))
		for i, sessionID := range sessionIDs {
			if _, ok := precomputed[sessionID]; !ok {
				continue
			}
			filteredIDs = append(filteredIDs, sessionID)
			filteredTS = append(filteredTS, timestamps[i])
		}
		sessionIDs = filteredIDs
		timestamps = filteredTS
		if len(sessionIDs) == 0 {
			return nil, nil
		}
		if isAssistantReference(question) {
			for sessionIndex, sessionID := range sessionIDs {
				data := precomputed[sessionID]
				for i, text := range data.FullChunks {
					candidates = append(candidates, lmeCandidate{SessionIndex: sessionIndex, SessionID: sessionID, Timestamp: data.Timestamp, LowerText: strings.ToLower(text), Similarity: cosineNormalized(queryEmbedding, data.FullEmbeddings[i]), IsPreference: false})
				}
			}
		} else {
			for sessionIndex, sessionID := range sessionIDs {
				data := precomputed[sessionID]
				for i, text := range data.UserChunks {
					candidates = append(candidates, lmeCandidate{SessionIndex: sessionIndex, SessionID: sessionID, Timestamp: data.Timestamp, LowerText: strings.ToLower(text), Similarity: cosineNormalized(queryEmbedding, data.UserEmbeddings[i]), IsPreference: false})
				}
				for i, text := range data.PrefChunks {
					candidates = append(candidates, lmeCandidate{SessionIndex: sessionIndex, SessionID: sessionID, Timestamp: data.Timestamp, LowerText: strings.ToLower(text), Similarity: cosineNormalized(queryEmbedding, data.PrefEmbeddings[i]), IsPreference: true})
				}
			}
		}
	} else {
		userSessions := make([]string, 0, len(entry.HaystackSessions))
		fullSessions := make([]string, 0, len(entry.HaystackSessions))
		prefDocs := make([]string, 0)
		prefIDs := make([]string, 0)
		prefTS := make([]string, 0)
		filteredIDs := make([]string, 0, len(entry.HaystackSessions))
		filteredTS := make([]string, 0, len(entry.HaystackSessions))
		for i, session := range entry.HaystackSessions {
			userText := joinUserTurns(session)
			if userText == "" {
				continue
			}
			filteredIDs = append(filteredIDs, entry.HaystackSessionIDs[i])
			filteredTS = append(filteredTS, entry.HaystackDates[i])
			userSessions = append(userSessions, userText)
			fullSessions = append(fullSessions, joinAllTurns(session))
			prefs := extractPreferences(session)
			if len(prefs) > 0 {
				prefDocs = append(prefDocs, "User has mentioned: "+strings.Join(prefs, "; "))
				prefIDs = append(prefIDs, entry.HaystackSessionIDs[i])
				prefTS = append(prefTS, entry.HaystackDates[i])
			}
		}
		sessionIDs = filteredIDs
		timestamps = filteredTS
		if len(sessionIDs) == 0 {
			return nil, nil
		}
		if isAssistantReference(question) {
			chunkTexts, chunkMeta := buildLMECandidatesWithLimit(fullSessions, sessionIDs, timestamps, false, 5)
			sims, err := rankTextCandidates(ctx, provider, question, queryEmbedding, chunkTexts, cache)
			if err != nil {
				return nil, err
			}
			for i, meta := range chunkMeta {
				candidates = append(candidates, lmeCandidate{SessionIndex: meta.SessionIndex, SessionID: meta.SessionID, Timestamp: meta.Timestamp, LowerText: strings.ToLower(chunkTexts[i]), Similarity: sims[i], IsPreference: false})
			}
		} else {
			chunkTexts, chunkMeta := buildLMECandidates(userSessions, sessionIDs, timestamps, false)
			prefTexts, prefMeta := buildLMECandidates(prefDocs, prefIDs, prefTS, true)
			allTexts := append(chunkTexts, prefTexts...)
			allMeta := append(chunkMeta, prefMeta...)
			sims, err := rankTextCandidates(ctx, provider, question, queryEmbedding, allTexts, cache)
			if err != nil {
				return nil, err
			}
			for i, meta := range allMeta {
				candidates = append(candidates, lmeCandidate{SessionIndex: meta.SessionIndex, SessionID: meta.SessionID, Timestamp: meta.Timestamp, LowerText: strings.ToLower(allTexts[i]), Similarity: sims[i], IsPreference: meta.IsPreference})
			}
		}
	}

	type scored struct {
		SessionIndex int
		SessionID    string
		Distance     float64
	}
	bestBySession := map[string]scored{}
	for _, candidate := range candidates {
		distance := retrieval.FuseSimilarity(float32(candidate.Similarity), keywordOverlapLower(questionKeywords, candidate.LowerText))
		if entry.QuestionType == "single-session-preference" && candidate.IsPreference {
			distance *= 0.90
		}
		if len(quotedPhrases) > 0 {
			if boost := quotedPhraseBoostLower(quotedPhrases, candidate.LowerText); boost > 0 {
				distance *= (1.0 - 0.60*boost)
			}
		}
		if len(personNames) > 0 {
			if boost := personNameBoostLower(personNames, candidate.LowerText); boost > 0 {
				distance *= (1.0 - 0.40*boost)
			}
		}
		if !targetDate.IsZero() {
			if boost := temporalBoost(parseLMEQuestionDate(candidate.Timestamp), targetDate, tolerance); boost > 0 {
				distance *= (1.0 - boost)
			}
		}
		current, ok := bestBySession[candidate.SessionID]
		if !ok || distance < current.Distance {
			bestBySession[candidate.SessionID] = scored{SessionIndex: candidate.SessionIndex, SessionID: candidate.SessionID, Distance: distance}
		}
	}
	ranked := make([]scored, 0, len(bestBySession))
	for _, item := range bestBySession {
		ranked = append(ranked, item)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Distance == ranked[j].Distance {
			return ranked[i].SessionID < ranked[j].SessionID
		}
		return ranked[i].Distance < ranked[j].Distance
	})
	out := make([]string, 0, len(ranked))
	seen := map[string]struct{}{}
	for _, item := range ranked {
		if _, ok := seen[item.SessionID]; ok {
			continue
		}
		seen[item.SessionID] = struct{}{}
		out = append(out, item.SessionID)
	}
	for _, sessionID := range sessionIDs {
		if _, ok := seen[sessionID]; ok {
			continue
		}
		seen[sessionID] = struct{}{}
		out = append(out, sessionID)
	}
	return out, nil
}

type lmeMeta struct {
	SessionIndex int
	SessionID    string
	Timestamp    string
	IsPreference bool
}

const lmeChunkSize = 250
const lmeChunkOverlap = 40
const lmeBatchSize = 16

type lmeEmbeddingCache struct {
	mu      sync.RWMutex
	vectors map[string][]float32
	limit   int
}

func newLMEEmbeddingCache() *lmeEmbeddingCache {
	return &lmeEmbeddingCache{vectors: map[string][]float32{}, limit: 120000}
}

func (c *lmeEmbeddingCache) size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.vectors)
}

func precomputeLongMemEvalSessions(ctx context.Context, provider vector.Provider, entries []LongMemEvalEntry, cache *lmeEmbeddingCache) (map[string]*lmeSessionData, error) {
	unique := map[string]*lmeSessionData{}
	for _, entry := range entries {
		for i, session := range entry.HaystackSessions {
			sessionID := entry.HaystackSessionIDs[i]
			if _, ok := unique[sessionID]; ok {
				continue
			}
			userText := joinUserTurns(session)
			if userText == "" {
				continue
			}
			fullText := joinAllTurns(session)
			prefs := extractPreferences(session)
			prefText := ""
			if len(prefs) > 0 {
				prefText = "User has mentioned: " + strings.Join(prefs, "; ")
			}
			unique[sessionID] = &lmeSessionData{SessionID: sessionID, Timestamp: entry.HaystackDates[i], UserChunks: splitLMEDocument(userText), FullChunks: splitLMEDocument(fullText)}
			if prefText != "" {
				unique[sessionID].PrefChunks = splitLMEDocument(prefText)
			}
		}
	}
	userTexts := make([]string, 0)
	userRefs := make([]struct {
		id  string
		idx int
	}, 0)
	fullTexts := make([]string, 0)
	fullRefs := make([]struct {
		id  string
		idx int
	}, 0)
	prefTexts := make([]string, 0)
	prefRefs := make([]struct {
		id  string
		idx int
	}, 0)
	for id, data := range unique {
		for i, text := range data.UserChunks {
			userTexts = append(userTexts, text)
			userRefs = append(userRefs, struct {
				id  string
				idx int
			}{id: id, idx: i})
		}
		for i, text := range data.FullChunks {
			fullTexts = append(fullTexts, text)
			fullRefs = append(fullRefs, struct {
				id  string
				idx int
			}{id: id, idx: i})
		}
		for i, text := range data.PrefChunks {
			prefTexts = append(prefTexts, text)
			prefRefs = append(prefRefs, struct {
				id  string
				idx int
			}{id: id, idx: i})
		}
	}
	if vectors, err := embedMany(ctx, provider, userTexts, cache); err != nil {
		return nil, err
	} else {
		for i, ref := range userRefs {
			data := unique[ref.id]
			if len(data.UserEmbeddings) == 0 {
				data.UserEmbeddings = make([][]float32, len(data.UserChunks))
			}
			data.UserEmbeddings[ref.idx] = vectors[i]
		}
	}
	if vectors, err := embedMany(ctx, provider, fullTexts, cache); err != nil {
		return nil, err
	} else {
		for i, ref := range fullRefs {
			data := unique[ref.id]
			if len(data.FullEmbeddings) == 0 {
				data.FullEmbeddings = make([][]float32, len(data.FullChunks))
			}
			data.FullEmbeddings[ref.idx] = vectors[i]
		}
	}
	if vectors, err := embedMany(ctx, provider, prefTexts, cache); err != nil {
		return nil, err
	} else {
		for i, ref := range prefRefs {
			data := unique[ref.id]
			if len(data.PrefEmbeddings) == 0 {
				data.PrefEmbeddings = make([][]float32, len(data.PrefChunks))
			}
			data.PrefEmbeddings[ref.idx] = vectors[i]
		}
	}
	return unique, nil
}

func buildLMECandidates(texts, ids, timestamps []string, isPreference bool) ([]string, []lmeMeta) {
	if len(texts) == 0 {
		return nil, nil
	}
	chunkTexts := make([]string, 0)
	chunkMeta := make([]lmeMeta, 0)
	for i, text := range texts {
		pieces := splitLMEDocument(text)
		for _, piece := range pieces {
			chunkTexts = append(chunkTexts, piece)
			chunkMeta = append(chunkMeta, lmeMeta{SessionIndex: i, SessionID: ids[i], Timestamp: timestamps[i], IsPreference: isPreference})
		}
	}
	return chunkTexts, chunkMeta
}

func buildLMECandidatesWithLimit(texts, ids, timestamps []string, isPreference bool, chunkLimit int) ([]string, []lmeMeta) {
	if len(texts) == 0 {
		return nil, nil
	}
	chunkTexts := make([]string, 0)
	chunkMeta := make([]lmeMeta, 0)
	for i, text := range texts {
		pieces := splitLMEDocument(text)
		if chunkLimit > 0 && len(pieces) > chunkLimit {
			pieces = pieces[:chunkLimit]
		}
		for _, piece := range pieces {
			chunkTexts = append(chunkTexts, piece)
			chunkMeta = append(chunkMeta, lmeMeta{SessionIndex: i, SessionID: ids[i], Timestamp: timestamps[i], IsPreference: isPreference})
		}
	}
	return chunkTexts, chunkMeta
}

func rankTextCandidates(ctx context.Context, provider vector.Provider, query string, queryEmbedding []float32, texts []string, cache *lmeEmbeddingCache) ([]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if len(queryEmbedding) == 0 {
		var err error
		queryEmbedding, err = embedOne(ctx, provider, query)
		if err != nil {
			return nil, err
		}
	}
	docEmbeddings, err := embedMany(ctx, provider, texts, cache)
	if err != nil {
		return nil, err
	}
	scores := make([]float64, 0, len(docEmbeddings))
	for _, embedding := range docEmbeddings {
		scores = append(scores, cosineNormalized(queryEmbedding, embedding))
	}
	return scores, nil
}

func embedOne(ctx context.Context, provider vector.Provider, text string) ([]float32, error) {
	return provider.Func(ctx, text)
}

func embedMany(ctx context.Context, provider vector.Provider, texts []string, cache *lmeEmbeddingCache) ([][]float32, error) {
	if cache == nil {
		return embedManyUncached(ctx, provider, texts)
	}
	out := make([][]float32, len(texts))
	missingTexts := make([]string, 0)
	missingIndexes := make([]int, 0)
	cache.mu.RLock()
	for i, text := range texts {
		if vector, ok := cache.vectors[text]; ok {
			out[i] = vector
		} else {
			missingTexts = append(missingTexts, text)
			missingIndexes = append(missingIndexes, i)
		}
	}
	cache.mu.RUnlock()
	if len(missingTexts) == 0 {
		return out, nil
	}
	vectors, err := embedManyUncached(ctx, provider, missingTexts)
	if err != nil {
		return nil, err
	}
	cache.mu.Lock()
	for i, index := range missingIndexes {
		out[index] = vectors[i]
		if cache.limit <= 0 || len(cache.vectors) < cache.limit {
			cache.vectors[missingTexts[i]] = vectors[i]
		}
	}
	cache.mu.Unlock()
	return out, nil
}

func embedManyUncached(ctx context.Context, provider vector.Provider, texts []string) ([][]float32, error) {
	if provider.Batch != nil {
		out := make([][]float32, 0, len(texts))
		for start := 0; start < len(texts); start += lmeBatchSize {
			end := start + lmeBatchSize
			if end > len(texts) {
				end = len(texts)
			}
			batch, err := provider.Batch(ctx, texts[start:end])
			if err != nil {
				return nil, err
			}
			out = append(out, batch...)
		}
		return out, nil
	}
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		vector, err := provider.Func(ctx, text)
		if err != nil {
			return nil, err
		}
		out = append(out, vector)
	}
	return out, nil
}

func splitLMEDocument(document string) []string {
	document = strings.TrimSpace(document)
	if len(document) <= lmeChunkSize {
		return []string{document}
	}
	chunks := []string{}
	for start := 0; start < len(document); {
		end := start + lmeChunkSize
		if end > len(document) {
			end = len(document)
		}
		if end < len(document) {
			if split := strings.LastIndex(document[start:end], "\n"); split > lmeChunkSize/2 {
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
		start = end - lmeChunkOverlap
		if start < 0 {
			start = 0
		}
	}
	return chunks
}

func cosineNormalized(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	total := float64(0)
	for i := range a {
		total += float64(a[i] * b[i])
	}
	return total
}

func joinAllTurns(turns []Turn) string {
	parts := make([]string, 0, len(turns))
	for _, turn := range turns {
		if turn.Content == "" {
			continue
		}
		parts = append(parts, turn.Content)
	}
	return strings.Join(parts, "\n")
}

func (e LongMemEvalEntry) QuestionDate() string { return e.QuestionDateRaw }

func recallAnyIDsAt(rankings []string, correctIDs map[string]struct{}, k int) float64 {
	for i, id := range rankings {
		if i >= k {
			break
		}
		if _, ok := correctIDs[id]; ok {
			return 1
		}
	}
	return 0
}

func reciprocalRankIDs(rankings []string, correctIDs map[string]struct{}) float64 {
	for i, id := range rankings {
		if _, ok := correctIDs[id]; ok {
			return 1 / float64(i+1)
		}
	}
	return 0
}

func ndcgIDs(rankings []string, correctIDs map[string]struct{}, k int) float64 {
	relevances := make([]float64, 0, min(k, len(rankings)))
	for i, id := range rankings {
		if i >= k {
			break
		}
		if _, ok := correctIDs[id]; ok {
			relevances = append(relevances, 1)
		} else {
			relevances = append(relevances, 0)
		}
	}
	idealCount := len(correctIDs)
	if idealCount > k {
		idealCount = k
	}
	ideal := make([]float64, idealCount)
	for i := 0; i < idealCount; i++ {
		ideal[i] = 1
	}
	idcg := dcg(ideal, k)
	if idcg == 0 {
		return 0
	}
	return dcg(relevances, k) / idcg
}

var quotedPhrasePatternSingle = regexp.MustCompile(`'([^']{3,60})'`)
var quotedPhrasePatternDouble = regexp.MustCompile(`"([^"]{3,60})"`)
var personNamePattern = regexp.MustCompile(`\b[A-Z][a-z]{2,15}\b`)

func extractQuotedPhrases(text string) []string {
	parts := append(quotedPhrasePatternSingle.FindAllStringSubmatch(text, -1), quotedPhrasePatternDouble.FindAllStringSubmatch(text, -1)...)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 2 {
			continue
		}
		value := strings.TrimSpace(part[1])
		if len(value) >= 3 {
			out = append(out, value)
		}
	}
	return out
}

var notNames = map[string]struct{}{
	"What": {}, "When": {}, "Where": {}, "Who": {}, "How": {}, "Which": {}, "Did": {}, "Do": {}, "Was": {}, "Were": {},
	"Have": {}, "Has": {}, "Had": {}, "Is": {}, "Are": {}, "The": {}, "My": {}, "Our": {}, "Their": {}, "Can": {}, "Could": {},
	"Would": {}, "Should": {}, "Will": {}, "Shall": {}, "May": {}, "Might": {}, "Monday": {}, "Tuesday": {}, "Wednesday": {},
	"Thursday": {}, "Friday": {}, "Saturday": {}, "Sunday": {}, "January": {}, "February": {}, "March": {}, "April": {}, "June": {},
	"July": {}, "August": {}, "September": {}, "October": {}, "November": {}, "December": {}, "In": {}, "On": {}, "At": {}, "For": {},
	"To": {}, "Of": {}, "With": {}, "By": {}, "From": {}, "And": {}, "But": {}, "I": {}, "It": {}, "Its": {}, "This": {}, "That": {},
	"These": {}, "Those": {}, "Previously": {}, "Recently": {}, "Also": {}, "Just": {}, "Very": {}, "More": {},
}

func extractPersonNames(text string) []string {
	words := personNamePattern.FindAllString(text, -1)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(words))
	for _, word := range words {
		if _, blocked := notNames[word]; blocked {
			continue
		}
		if _, ok := seen[word]; ok {
			continue
		}
		seen[word] = struct{}{}
		out = append(out, word)
	}
	return out
}

func parseLMEQuestionDate(dateStr string) time.Time {
	dateStr = strings.TrimSpace(strings.Split(dateStr, " (")[0])
	if dateStr == "" {
		return time.Time{}
	}
	parsed, err := time.Parse("2006/01/02", dateStr)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func parseTimeOffsetDays(question string) (int, int, bool) {
	q := strings.ToLower(question)
	patterns := []struct {
		pattern *regexp.Regexp
		days    func([]string) int
		tol     int
	}{
		{regexp.MustCompile(`(\d+)\s+days?\s+ago`), func(m []string) int { return atoiSafe(m[1]) }, 2},
		{regexp.MustCompile(`a\s+couple\s+(?:of\s+)?days?\s+ago`), func([]string) int { return 2 }, 2},
		{regexp.MustCompile(`yesterday`), func([]string) int { return 1 }, 1},
		{regexp.MustCompile(`a\s+week\s+ago`), func([]string) int { return 7 }, 3},
		{regexp.MustCompile(`(\d+)\s+weeks?\s+ago`), func(m []string) int { return atoiSafe(m[1]) * 7 }, 5},
		{regexp.MustCompile(`last\s+week`), func([]string) int { return 7 }, 3},
		{regexp.MustCompile(`a\s+month\s+ago`), func([]string) int { return 30 }, 7},
		{regexp.MustCompile(`(\d+)\s+months?\s+ago`), func(m []string) int { return atoiSafe(m[1]) * 30 }, 10},
		{regexp.MustCompile(`last\s+month`), func([]string) int { return 30 }, 7},
		{regexp.MustCompile(`last\s+year`), func([]string) int { return 365 }, 30},
		{regexp.MustCompile(`a\s+year\s+ago`), func([]string) int { return 365 }, 30},
		{regexp.MustCompile(`recently`), func([]string) int { return 14 }, 14},
	}
	for _, item := range patterns {
		matches := item.pattern.FindStringSubmatch(q)
		if len(matches) > 0 {
			return item.days(matches), item.tol, true
		}
	}
	return 0, 0, false
}

func isAssistantReference(question string) bool {
	q := strings.ToLower(question)
	triggers := []string{"you suggested", "you told me", "you mentioned", "you said", "you recommended", "remind me what you", "you provided", "you listed", "you gave me", "you described", "what did you", "you came up with", "you helped me", "you explained", "can you remind me", "you identified"}
	for _, trigger := range triggers {
		if strings.Contains(q, trigger) {
			return true
		}
	}
	return false
}

var prefPatterns = []*regexp.Regexp{
	regexp.MustCompile(`i(?:'ve been| have been) having (?:trouble|issues?|problems?) with ([^,\.!?]{5,80})`),
	regexp.MustCompile(`i(?:'ve been| have been) feeling ([^,\.!?]{5,60})`),
	regexp.MustCompile(`i(?:'ve been| have been) (?:struggling|dealing) with ([^,\.!?]{5,80})`),
	regexp.MustCompile(`i(?:'ve been| have been) (?:worried|concerned) about ([^,\.!?]{5,80})`),
	regexp.MustCompile(`i(?:'m| am) (?:worried|concerned) about ([^,\.!?]{5,80})`),
	regexp.MustCompile(`i prefer ([^,\.!?]{5,60})`),
	regexp.MustCompile(`i like ([^,\.!?]{3,60})`),
	regexp.MustCompile(`i love ([^,\.!?]{3,60})`),
	regexp.MustCompile(`i enjoy ([^,\.!?]{3,60})`),
	regexp.MustCompile(`i(?:'m| am) a fan of ([^,\.!?]{3,60})`),
	regexp.MustCompile(`my favorite(?: [^,\.!?]{1,20})? is ([^,\.!?]{3,60})`),
	regexp.MustCompile(`i(?:'m| am) into ([^,\.!?]{3,60})`),
	regexp.MustCompile(`i don't like ([^,\.!?]{3,60})`),
	regexp.MustCompile(`i dislike ([^,\.!?]{3,60})`),
	regexp.MustCompile(`i usually ([^,\.!?]{5,60})`),
	regexp.MustCompile(`i(?:'ve been| have been) (?:trying|attempting) to ([^,\.!?]{5,80})`),
	regexp.MustCompile(`i(?:'ve been| have been) (?:considering|thinking about) ([^,\.!?]{5,80})`),
	regexp.MustCompile(`lately[,\s]+(?:i've been|i have been|i'm|i am) ([^,\.!?]{5,80})`),
	regexp.MustCompile(`recently[,\s]+(?:i've been|i have been|i'm|i am) ([^,\.!?]{5,80})`),
	regexp.MustCompile(`i(?:'ve been| have been) (?:working on|focused on|interested in) ([^,\.!?]{5,80})`),
	regexp.MustCompile(`i want to ([^,\.!?]{5,60})`),
	regexp.MustCompile(`i(?:'m| am) looking (?:to|for) ([^,\.!?]{5,60})`),
	regexp.MustCompile(`i(?:'m| am) thinking (?:about|of) ([^,\.!?]{5,60})`),
	regexp.MustCompile(`i(?:'ve been| have been) (?:noticing|experiencing) ([^,\.!?]{5,80})`),
	regexp.MustCompile(`i (?:still )?remember (?:the |my )?([^,\.!?]{5,80})`),
	regexp.MustCompile(`i used to ([^,\.!?]{5,60})`),
	regexp.MustCompile(`when i was (?:in high school|in college|young|a kid|growing up)[,\s]+([^,\.!?]{5,80})`),
	regexp.MustCompile(`growing up[,\s]+([^,\.!?]{5,80})`),
	regexp.MustCompile(`(?:happy|fond|good|positive) (?:high school|college|childhood|school) (?:experience|memory|memories|time)[^,\.!?]{0,60}`),
}

func extractPreferences(session []Turn) []string {
	mentions := []string{}
	for _, turn := range session {
		if turn.Role != "user" {
			continue
		}
		text := strings.ToLower(turn.Content)
		for _, pattern := range prefPatterns {
			matches := pattern.FindAllStringSubmatch(text, -1)
			for _, match := range matches {
				candidate := match[0]
				if len(match) > 1 && strings.TrimSpace(match[1]) != "" {
					candidate = match[1]
				}
				candidate = strings.TrimSpace(strings.TrimRight(candidate, ".,;!? "))
				if len(candidate) >= 5 && len(candidate) <= 80 {
					mentions = append(mentions, candidate)
				}
			}
		}
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(mentions))
	for _, mention := range mentions {
		if _, ok := seen[mention]; ok {
			continue
		}
		seen[mention] = struct{}{}
		out = append(out, mention)
		if len(out) >= 12 {
			break
		}
	}
	return out
}

func quotedPhraseBoost(phrases []string, docText string) float64 {
	if len(phrases) == 0 {
		return 0
	}
	docLower := strings.ToLower(docText)
	hits := 0
	for _, phrase := range phrases {
		if strings.Contains(docLower, strings.ToLower(phrase)) {
			hits++
		}
	}
	return minFloat64(float64(hits)/float64(len(phrases)), 1.0)
}

func quotedPhraseBoostLower(phrases []string, docLower string) float64 {
	if len(phrases) == 0 {
		return 0
	}
	hits := 0
	for _, phrase := range phrases {
		if strings.Contains(docLower, strings.ToLower(phrase)) {
			hits++
		}
	}
	return minFloat64(float64(hits)/float64(len(phrases)), 1.0)
}

func personNameBoost(names []string, docText string) float64 {
	if len(names) == 0 {
		return 0
	}
	docLower := strings.ToLower(docText)
	hits := 0
	for _, name := range names {
		if strings.Contains(docLower, strings.ToLower(name)) {
			hits++
		}
	}
	return minFloat64(float64(hits)/float64(len(names)), 1.0)
}

func personNameBoostLower(names []string, docLower string) float64 {
	if len(names) == 0 {
		return 0
	}
	hits := 0
	for _, name := range names {
		if strings.Contains(docLower, strings.ToLower(name)) {
			hits++
		}
	}
	return minFloat64(float64(hits)/float64(len(names)), 1.0)
}

func keywordOverlapLower(queryKeywords []string, docLower string) float64 {
	if len(queryKeywords) == 0 {
		return 0
	}
	hits := 0
	for _, keyword := range queryKeywords {
		if strings.Contains(docLower, keyword) {
			hits++
		}
	}
	return float64(hits) / float64(len(queryKeywords))
}

func temporalBoost(sessionDate, targetDate time.Time, tolerance int) float64 {
	if sessionDate.IsZero() || targetDate.IsZero() || tolerance <= 0 {
		return 0
	}
	deltaDays := intAbs(int(sessionDate.Sub(targetDate).Hours() / 24))
	if deltaDays <= tolerance {
		return 0.40
	}
	if deltaDays <= tolerance*3 {
		return 0.40 * (1.0 - float64(deltaDays-tolerance)/float64(tolerance*2))
	}
	return 0
}

func atoiSafe(value string) int {
	value = strings.TrimSpace(value)
	result := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			break
		}
		result = result*10 + int(r-'0')
	}
	return result
}

func minFloat64(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

func intAbs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func joinUserTurns(turns []Turn) string {
	parts := make([]string, 0, len(turns))
	for _, turn := range turns {
		if turn.Role != "user" {
			continue
		}
		if turn.Content == "" {
			continue
		}
		parts = append(parts, turn.Content)
	}
	return strings.Join(parts, "\n")
}

func sliceToSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func FormatLongMemEval(result LongMemEvalResult) string {
	var builder strings.Builder
	builder.WriteString("LongMemEval\n\n")
	builder.WriteString(fmt.Sprintf("Questions:   %d\n", result.Questions))
	builder.WriteString(fmt.Sprintf("Recall@1:   %.3f\n", result.RecallAt1))
	builder.WriteString(fmt.Sprintf("Recall@5:   %.3f\n", result.RecallAt5))
	builder.WriteString(fmt.Sprintf("Recall@10:  %.3f\n", result.RecallAt10))
	builder.WriteString(fmt.Sprintf("MRR:        %.3f\n", result.MRR))
	builder.WriteString(fmt.Sprintf("NDCG@10:    %.3f\n", result.NDCGAt10))
	builder.WriteString(fmt.Sprintf("Time:       %.1fs\n", result.ElapsedSeconds))
	builder.WriteString("\nDistribution:\n")
	builder.WriteString(fmt.Sprintf("  hit@1:  %d\n", result.Distribution["hit@1"]))
	builder.WriteString(fmt.Sprintf("  hit@5:  %d\n", result.Distribution["hit@5"]))
	builder.WriteString(fmt.Sprintf("  miss@5: %d\n", result.Distribution["miss@5"]))
	if len(result.PerQuestionType) > 0 {
		builder.WriteString("\nPer question type:\n")
		keys := make([]string, 0, len(result.PerQuestionType))
		for key := range result.PerQuestionType {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			builder.WriteString(fmt.Sprintf("  %s: %.3f\n", key, result.PerQuestionType[key]))
		}
	}
	return builder.String()
}

func WriteLongMemEvalResult(path string, result LongMemEvalResult) error {
	return writeJSON(path, result)
}

func reciprocalRank(rankings []int, correctIDs map[string]struct{}, corpusIDs []string) float64 {
	for i, idx := range rankings {
		if _, ok := correctIDs[corpusIDs[idx]]; ok {
			return 1 / float64(i+1)
		}
	}
	return 0
}
