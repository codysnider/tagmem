package bench

import "math"

func dcg(relevances []float64, k int) float64 {
	score := 0.0
	for i, rel := range relevances {
		if i >= k {
			break
		}
		score += rel / math.Log2(float64(i+2))
	}
	return score
}

func ndcg(rankings []int, correctIDs map[string]struct{}, corpusIDs []string, k int) float64 {
	relevances := make([]float64, 0, k)
	for i, idx := range rankings {
		if i >= k {
			break
		}
		_, ok := correctIDs[corpusIDs[idx]]
		if ok {
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

func recallAnyAt(rankings []int, correctIDs map[string]struct{}, corpusIDs []string, k int) float64 {
	for i, idx := range rankings {
		if i >= k {
			break
		}
		if _, ok := correctIDs[corpusIDs[idx]]; ok {
			return 1
		}
	}
	return 0
}
