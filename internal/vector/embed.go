package vector

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"unicode"

	chromem "github.com/philippgille/chromem-go"
)

const dimensions = 384

func embeddedEmbeddingFunc() chromem.EmbeddingFunc {
	return func(_ context.Context, text string) ([]float32, error) {
		vector := make([]float32, dimensions)
		tokens := tokenize(text)
		for _, token := range tokens {
			addHashedFeature(vector, token, 1.0)
			for _, trigram := range trigrams(token) {
				addHashedFeature(vector, trigram, 0.35)
			}
		}

		normalize(vector)
		return vector, nil
	}
}

func tokenize(text string) []string {
	text = strings.ToLower(text)
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})

	tokens := make([]string, 0, len(parts))
	for _, part := range parts {
		if len(part) < 2 {
			continue
		}
		tokens = append(tokens, part)
	}

	return tokens
}

func trigrams(token string) []string {
	if len(token) < 3 {
		return nil
	}

	grams := make([]string, 0, len(token)-2)
	for i := 0; i <= len(token)-3; i++ {
		grams = append(grams, token[i:i+3])
	}
	return grams
}

func addHashedFeature(vector []float32, token string, weight float32) {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(token))
	sum := hash.Sum32()
	index := int(sum % dimensions)
	if sum&1 == 0 {
		vector[index] += weight
		return
	}
	vector[index] -= weight
}

func normalize(vector []float32) {
	var magnitude float64
	for _, value := range vector {
		magnitude += float64(value * value)
	}
	if magnitude == 0 {
		return
	}

	inv := float32(1 / math.Sqrt(magnitude))
	for i := range vector {
		vector[i] *= inv
	}
}
