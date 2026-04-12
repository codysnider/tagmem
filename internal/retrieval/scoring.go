package retrieval

import (
	"math"
	"strings"
	"time"
)

const HybridWeight = 0.30

var stopWords = map[string]struct{}{
	"what": {}, "when": {}, "where": {}, "who": {}, "how": {}, "which": {},
	"did": {}, "do": {}, "was": {}, "were": {}, "have": {}, "has": {}, "had": {},
	"is": {}, "are": {}, "the": {}, "a": {}, "an": {}, "my": {}, "me": {},
	"i": {}, "you": {}, "your": {}, "their": {}, "it": {}, "its": {}, "in": {},
	"on": {}, "at": {}, "to": {}, "for": {}, "of": {}, "with": {}, "by": {},
	"from": {}, "ago": {}, "last": {}, "that": {}, "this": {}, "there": {},
	"about": {}, "get": {}, "got": {}, "give": {}, "gave": {}, "buy": {},
	"bought": {}, "made": {}, "make": {}, "said": {},
}

func ExtractKeywords(text string) []string {
	parts := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})

	keywords := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 3 {
			continue
		}
		if _, ok := stopWords[part]; ok {
			continue
		}
		keywords = append(keywords, part)
	}

	if len(keywords) > 0 {
		return keywords
	}

	return strings.Fields(strings.ToLower(strings.TrimSpace(text)))
}

func KeywordOverlap(queryKeywords []string, document string) float64 {
	if len(queryKeywords) == 0 {
		return 0
	}

	document = strings.ToLower(document)
	hits := 0
	for _, keyword := range queryKeywords {
		if strings.Contains(document, keyword) {
			hits++
		}
	}

	return float64(hits) / float64(len(queryKeywords))
}

func FuseSimilarity(similarity float32, overlap float64) float64 {
	return (1 - float64(similarity)) * (1 - HybridWeight*overlap)
}

func RecencyPenalty(updatedAt time.Time, now time.Time) float64 {
	if updatedAt.IsZero() || now.IsZero() {
		return 1.0
	}
	nowMicros := now.UnixMicro()
	updatedMicros := updatedAt.UnixMicro()
	if updatedMicros <= 0 || nowMicros <= updatedMicros {
		return 1.0
	}
	ageDays := float64(nowMicros-updatedMicros) / float64(24*time.Hour/time.Microsecond)
	penalty := 1.0 + 0.08*(1-math.Exp(-ageDays/120.0))
	if penalty < 1.0 {
		return 1.0
	}
	if penalty > 1.08 {
		return 1.08
	}
	return penalty
}

func ReinforcementPenalty(supportCount int, sourceDiversity int) float64 {
	if supportCount <= 1 {
		return 1.0
	}
	boost := 0.0
	if supportCount >= 2 {
		boost += 0.04
	}
	if supportCount >= 3 {
		boost += 0.03
	}
	if supportCount >= 5 {
		boost += 0.03
	}
	if sourceDiversity >= 2 {
		boost += 0.03
	}
	if sourceDiversity >= 3 {
		boost += 0.02
	}
	penalty := 1.0 - boost
	if penalty < 0.82 {
		return 0.82
	}
	return penalty
}

func ContradictionPenalty(conflictCount int) float64 {
	if conflictCount <= 0 {
		return 1.0
	}
	penalty := 1.0 + 0.05*float64(conflictCount)
	if penalty > 1.18 {
		return 1.18
	}
	return penalty
}
