package tagging

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/codysnider/tagmem/internal/vector"
)

func TestRankCandidatesUsesSingleBatchForContentAndLabels(t *testing.T) {
	candidates := []Candidate{{Label: "alpha"}, {Label: "beta"}}
	batchCalls := 0
	provider := &vector.Provider{
		Func: func(context.Context, string) ([]float32, error) {
			return nil, errors.New("Func should not be called")
		},
		Batch: func(_ context.Context, texts []string) ([][]float32, error) {
			batchCalls++
			wantTexts := []string{"content", "alpha", "beta"}
			if !reflect.DeepEqual(texts, wantTexts) {
				t.Fatalf("Batch texts = %v, want %v", texts, wantTexts)
			}
			return [][]float32{{1, 0}, {1, 0}, {0, 1}}, nil
		},
	}

	rankCandidates(candidates, "content", provider)

	if batchCalls != 1 {
		t.Fatalf("Batch calls = %d, want 1", batchCalls)
	}
	if candidates[0].Score != 4 {
		t.Fatalf("candidates[0].Score = %v, want 4", candidates[0].Score)
	}
	if candidates[1].Score != 0 {
		t.Fatalf("candidates[1].Score = %v, want 0", candidates[1].Score)
	}
}

func TestRankCandidatesKeepsAvailableSemanticScoresWhenBatchIsShort(t *testing.T) {
	candidates := []Candidate{{Label: "alpha"}, {Label: "beta"}}
	funcCalls := []string{}
	provider := &vector.Provider{
		Func: func(_ context.Context, text string) ([]float32, error) {
			funcCalls = append(funcCalls, text)
			switch text {
			case "beta":
				return []float32{1, 0}, nil
			default:
				t.Fatalf("unexpected Func text %q", text)
				return nil, nil
			}
		},
		Batch: func(_ context.Context, texts []string) ([][]float32, error) {
			wantTexts := []string{"content", "alpha", "beta"}
			if !reflect.DeepEqual(texts, wantTexts) {
				t.Fatalf("Batch texts = %v, want %v", texts, wantTexts)
			}
			return [][]float32{{1, 0}, {1, 0}}, nil
		},
	}

	rankCandidates(candidates, "content", provider)

	if candidates[0].Score != 4 {
		t.Fatalf("candidates[0].Score = %v, want 4", candidates[0].Score)
	}
	if candidates[1].Score != 4 {
		t.Fatalf("candidates[1].Score = %v, want 4", candidates[1].Score)
	}
	if !reflect.DeepEqual(funcCalls, []string{"beta"}) {
		t.Fatalf("Func calls = %v, want %v", funcCalls, []string{"beta"})
	}
}
