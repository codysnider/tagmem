package vector

import (
	"context"

	chromem "github.com/philippgille/chromem-go"
)

func EmbeddedHashProvider() Provider {
	return Provider{
		Name:        ProviderEmbedded,
		IndexKey:    ProviderEmbedded + "-hash-v1",
		Description: "embedded hash fallback embedding",
		Model:       "embedded-hash-v1",
		Func:        embeddedEmbeddingFunc(),
		Batch: func(ctx context.Context, texts []string) ([][]float32, error) {
			out := make([][]float32, 0, len(texts))
			fn := embeddedEmbeddingFunc()
			for _, text := range texts {
				vector, err := fn(ctx, text)
				if err != nil {
					return nil, err
				}
				out = append(out, vector)
			}
			return out, nil
		},
	}
}

func (e *miniLMEmbedder) EmbeddingFunc() chromem.EmbeddingFunc {
	return func(_ context.Context, text string) ([]float32, error) {
		return e.Embed(text)
	}
}
