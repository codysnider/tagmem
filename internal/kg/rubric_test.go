package kg

import "testing"

func TestAssessFactPromotionCanonicalConfig(t *testing.T) {
	t.Parallel()

	assessment := AssessFactPromotion("Staging uses postgres.internal.example.com.")
	if !assessment.StoreAsFact {
		t.Fatal("expected canonical config statement to be promoted to fact")
	}
	if assessment.KeepAsEntry {
		t.Fatal("expected canonical config statement to not require keeping the entry")
	}
	if assessment.Predicate != "uses" {
		t.Fatalf("assessment.Predicate = %q, want uses", assessment.Predicate)
	}
	if assessment.PredicateFamily != "config" {
		t.Fatalf("assessment.PredicateFamily = %q, want config", assessment.PredicateFamily)
	}
	if assessment.Object != "postgres.internal.example.com" {
		t.Fatalf("assessment.Object = %q, want postgres.internal.example.com", assessment.Object)
	}
}

func TestAssessFactPromotionRejectsSoftClaims(t *testing.T) {
	t.Parallel()

	assessment := AssessFactPromotion("We discussed maybe moving staging to postgres next quarter.")
	if assessment.StoreAsFact {
		t.Fatal("expected planning statement to stay out of the knowledge graph")
	}
	if !assessment.KeepAsEntry {
		t.Fatal("expected planning statement to stay as an entry")
	}
	if len(assessment.Reasons) == 0 {
		t.Fatal("expected reasons for rejection")
	}
}

func TestAssessFactPromotionKeepsTemporalNuance(t *testing.T) {
	t.Parallel()

	assessment := AssessFactPromotion("Caroline currently lives in New York.")
	if !assessment.StoreAsFact {
		t.Fatal("expected temporal location statement to remain fact-eligible")
	}
	if !assessment.KeepAsEntry {
		t.Fatal("expected temporal qualifier to keep the source entry too")
	}
	if assessment.Predicate != "lives_in" {
		t.Fatalf("assessment.Predicate = %q, want lives_in", assessment.Predicate)
	}
}
