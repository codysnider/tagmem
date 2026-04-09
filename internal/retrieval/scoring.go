package retrieval

import "strings"

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
