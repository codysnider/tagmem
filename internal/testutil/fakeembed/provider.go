package fakeembed

import (
	"context"
	"math"
	"strings"
	"unicode"

	"github.com/codysnider/tagmem/internal/vector"
	chromem "github.com/philippgille/chromem-go"
)

const dimensions = 384

func Provider() vector.Provider {
	fn := embeddingFunc()
	return vector.Provider{
		Name:        vector.ProviderEmbedded,
		IndexKey:    "test-fake-embed-v1",
		Description: "deterministic fake embedding provider for tests",
		Model:       "test-fake-embed-v1",
		Func:        fn,
		Batch: func(ctx context.Context, texts []string) ([][]float32, error) {
			vectors := make([][]float32, 0, len(texts))
			for _, text := range texts {
				vector, err := fn(ctx, text)
				if err != nil {
					return nil, err
				}
				vectors = append(vectors, vector)
			}
			return vectors, nil
		},
	}
}

func embeddingFunc() chromem.EmbeddingFunc {
	return func(_ context.Context, text string) ([]float32, error) {
		vector := make([]float32, dimensions)
		for _, token := range tokenize(text) {
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
	parts := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
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
	index, positive := fnvBucket(token)
	if positive {
		vector[index] += weight
		return
	}
	vector[index] -= weight
}

func fnvBucket(token string) (int, bool) {
	const (
		offset32 = 2166136261
		prime32  = 16777619
	)
	var sum uint32 = offset32
	for i := 0; i < len(token); i++ {
		sum ^= uint32(token[i])
		sum *= prime32
	}
	return int(sum % dimensions), sum&1 == 0
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
