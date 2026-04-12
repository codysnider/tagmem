package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
)

type baseline struct {
	Questions  int     `json:"questions"`
	RecallAt1  float64 `json:"recall_at_1"`
	RecallAt5  float64 `json:"recall_at_5"`
	RecallAt10 float64 `json:"recall_at_10"`
	MRR        float64 `json:"mrr"`
	NDCGAt10   float64 `json:"ndcg_at_10"`
	Tolerance  float64 `json:"tolerance"`
}

type result struct {
	Questions  int     `json:"questions"`
	RecallAt1  float64 `json:"recall_at_1"`
	RecallAt5  float64 `json:"recall_at_5"`
	RecallAt10 float64 `json:"recall_at_10"`
	MRR        float64 `json:"mrr"`
	NDCGAt10   float64 `json:"ndcg_at_10"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: compare_longmemeval <baseline-json> <result-json>")
		os.Exit(1)
	}

	base := mustLoadBaseline(os.Args[1])
	run := mustLoadResult(os.Args[2])

	if base.Questions > 0 && run.Questions != base.Questions {
		fmt.Fprintf(os.Stderr, "questions mismatch: got %d want %d\n", run.Questions, base.Questions)
		os.Exit(1)
	}

	tolerance := base.Tolerance
	if tolerance <= 0 {
		tolerance = 0.01
	}

	failures := []string{}
	check := func(name string, got, want float64) {
		if want-got > tolerance+1e-9 {
			failures = append(failures, fmt.Sprintf("%s regressed: got %.6f baseline %.6f tolerance %.3f", name, got, want, tolerance))
		}
	}

	check("recall_at_1", run.RecallAt1, base.RecallAt1)
	check("recall_at_5", run.RecallAt5, base.RecallAt5)
	check("recall_at_10", run.RecallAt10, base.RecallAt10)
	check("mrr", run.MRR, base.MRR)
	check("ndcg_at_10", run.NDCGAt10, base.NDCGAt10)

	if len(failures) > 0 {
		for _, failure := range failures {
			fmt.Fprintln(os.Stderr, failure)
		}
		os.Exit(1)
	}

	printMetric("recall_at_1", run.RecallAt1, base.RecallAt1)
	printMetric("recall_at_5", run.RecallAt5, base.RecallAt5)
	printMetric("recall_at_10", run.RecallAt10, base.RecallAt10)
	printMetric("mrr", run.MRR, base.MRR)
	printMetric("ndcg_at_10", run.NDCGAt10, base.NDCGAt10)
	fmt.Printf("guard passed with tolerance %.3f\n", tolerance)
}

func mustLoadBaseline(path string) baseline {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	var value baseline
	if err := json.Unmarshal(data, &value); err != nil {
		panic(err)
	}
	return value
}

func mustLoadResult(path string) result {
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	var value result
	if err := json.Unmarshal(data, &value); err != nil {
		panic(err)
	}
	return value
}

func printMetric(name string, got, want float64) {
	delta := got - want
	if math.Abs(delta) < 1e-9 {
		delta = 0
	}
	fmt.Printf("%s ok: got %.6f baseline %.6f delta %+0.6f\n", name, got, want, delta)
}
