package tagging

import (
	"context"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/codysnider/tagmem/internal/vector"
)

type Mode string

const (
	ModeFiles         Mode = "files"
	ModeConversations Mode = "conversations"
)

type Candidate struct {
	Canonical string
	Label     string
	Class     string
	Score     float64
}

var (
	acronymPattern      = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,15}\b`)
	camelPattern        = regexp.MustCompile(`\b[A-Za-z0-9]*[a-z][A-Z][A-Za-z0-9]*\b`)
	snakePattern        = regexp.MustCompile(`\b[a-z0-9]+(?:_[a-z0-9]+)+\b`)
	kebabPattern        = regexp.MustCompile(`\b[a-z0-9]+(?:-[a-z0-9]+)+\b`)
	quotedPatternDouble = regexp.MustCompile(`"([^"]{3,80})"`)
	quotedPatternSingle = regexp.MustCompile(`'([^']{3,80})'`)
	titlePhrasePattern  = regexp.MustCompile(`\b[A-Z][a-z0-9]+(?:\s+[A-Z][a-z0-9]+){0,3}\b`)
	issuePattern        = regexp.MustCompile(`\b(?:PR|Issue|Ticket|INC|BUG)-?\d+\b`)
	domainPattern       = regexp.MustCompile(`\b[a-z0-9.-]+\.[a-z]{2,}\b`)
	envPattern          = regexp.MustCompile(`\b[A-Z][A-Z0-9_]{2,}\b`)
)

var stopCanonical = map[string]struct{}{
	"the": {}, "this": {}, "that": {}, "with": {}, "from": {}, "what": {}, "when": {}, "where": {}, "which": {}, "there": {},
	"session": {}, "notes": {}, "note": {}, "chat": {}, "file": {}, "files": {}, "entry": {}, "entries": {},
}

func BuildTags(relPath, content string, mode Mode, provider *vector.Provider) []string {
	candidates := candidateMap{}
	addPathCandidates(candidates, relPath)
	addPatternCandidates(candidates, content)
	addModeCandidates(candidates, content, mode)
	ordered := candidates.list()
	rankCandidates(ordered, content, provider)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Score == ordered[j].Score {
			return ordered[i].Canonical < ordered[j].Canonical
		}
		return ordered[i].Score > ordered[j].Score
	})
	out := make([]string, 0, 12)
	for _, candidate := range ordered {
		if candidate.Canonical == "" {
			continue
		}
		out = append(out, candidate.Canonical)
		if len(out) >= 12 {
			break
		}
	}
	return out
}

type candidateMap map[string]*Candidate

func (m candidateMap) add(label, class string, score float64) {
	canonical := canonicalize(label)
	if canonical == "" {
		return
	}
	if _, blocked := stopCanonical[canonical]; blocked {
		return
	}
	if existing, ok := m[canonical]; ok {
		existing.Score += score
		return
	}
	m[canonical] = &Candidate{Canonical: canonical, Label: label, Class: class, Score: score}
}

func (m candidateMap) list() []Candidate {
	out := make([]Candidate, 0, len(m))
	for _, candidate := range m {
		out = append(out, *candidate)
	}
	return out
}

func addPathCandidates(candidates candidateMap, relPath string) {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	for _, part := range parts {
		stem := strings.TrimSuffix(part, filepath.Ext(part))
		candidates.add(stem, "path", 3.5)
	}
	if ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(relPath)), "."); ext != "" {
		candidates.add(ext, "extension", 1.0)
	}
}

func addPatternCandidates(candidates candidateMap, content string) {
	for _, match := range acronymPattern.FindAllString(content, -1) {
		candidates.add(match, "acronym", 3.0)
	}
	for _, match := range envPattern.FindAllString(content, -1) {
		candidates.add(match, "env", 2.8)
	}
	for _, match := range camelPattern.FindAllString(content, -1) {
		candidates.add(match, "symbol", 2.5)
	}
	for _, match := range snakePattern.FindAllString(content, -1) {
		candidates.add(match, "symbol", 2.2)
	}
	for _, match := range kebabPattern.FindAllString(content, -1) {
		candidates.add(match, "symbol", 2.2)
	}
	for _, match := range issuePattern.FindAllString(content, -1) {
		candidates.add(match, "issue", 2.0)
	}
	for _, match := range domainPattern.FindAllString(content, -1) {
		candidates.add(match, "domain", 1.8)
	}
	for _, matches := range [][]string{
		flattenSubmatches(quotedPatternDouble.FindAllStringSubmatch(content, -1)),
		flattenSubmatches(quotedPatternSingle.FindAllStringSubmatch(content, -1)),
	} {
		for _, match := range matches {
			candidates.add(match, "quoted", 2.4)
		}
	}
	for _, phrase := range titlePhrasePattern.FindAllString(content, -1) {
		phrase = strings.TrimSpace(phrase)
		if wordCount(phrase) == 0 || wordCount(phrase) > 4 {
			continue
		}
		candidates.add(phrase, "proper-noun", 2.6)
	}
}

func addModeCandidates(candidates candidateMap, content string, mode Mode) {
	contentLower := strings.ToLower(content)
	if mode == ModeConversations {
		for label, keywords := range map[string][]string{
			"decisions":   {"decided", "chose", "picked", "switched", "migrated", "replaced", "approach"},
			"preferences": {"i like", "i love", "i prefer", "favorite", "i enjoy", "i dislike"},
			"milestones":  {"finished", "completed", "launched", "shipped", "graduated", "started", "joined"},
			"problems":    {"problem", "issue", "broken", "failed", "workaround", "fix", "resolved"},
			"planning":    {"plan", "roadmap", "deadline", "priority", "backlog", "requirement", "spec"},
		} {
			for _, keyword := range keywords {
				if strings.Contains(contentLower, keyword) {
					candidates.add(label, "conversation-class", 2.0)
					break
				}
			}
		}
	}
}

func rankCandidates(candidates []Candidate, content string, provider *vector.Provider) {
	contentLower := strings.ToLower(content)
	for i := range candidates {
		mentions := strings.Count(contentLower, strings.ToLower(candidates[i].Label))
		if mentions > 0 {
			candidates[i].Score += float64(mentions) * 0.6
		}
	}
	if provider == nil || provider.Batch == nil {
		return
	}
	ctx := context.Background()
	contentVec, err := provider.Func(ctx, content)
	if err != nil {
		return
	}
	labels := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		labels = append(labels, candidate.Label)
	}
	vectors, err := provider.Batch(ctx, labels)
	if err != nil {
		return
	}
	for i := range candidates {
		candidates[i].Score += cosine(contentVec, vectors[i]) * 4.0
	}
}

func cosine(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	total := 0.0
	for i := range a {
		total += float64(a[i] * b[i])
	}
	return total
}

func canonicalize(label string) string {
	label = strings.TrimSpace(label)
	if label == "" {
		return ""
	}
	label = strings.ReplaceAll(label, "_", "-")
	label = strings.ReplaceAll(label, " ", "-")
	label = strings.ReplaceAll(label, ".", "-")
	label = strings.ReplaceAll(label, "/", "-")
	label = strings.ToLower(label)
	label = regexp.MustCompile(`[^a-z0-9-]+`).ReplaceAllString(label, "")
	label = regexp.MustCompile(`-+`).ReplaceAllString(label, "-")
	label = strings.Trim(label, "-")
	if len(label) < 2 {
		return ""
	}
	return label
}

func flattenSubmatches(matches [][]string) []string {
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		out = append(out, strings.TrimSpace(match[1]))
	}
	return out
}

func wordCount(text string) int {
	return len(strings.Fields(text))
}
