package kg

import (
	"regexp"
	"strings"
)

type FactPromotionAssessment struct {
	StoreAsFact     bool     `json:"store_as_fact"`
	KeepAsEntry     bool     `json:"keep_as_entry"`
	Confidence      string   `json:"confidence"`
	PredicateFamily string   `json:"predicate_family,omitempty"`
	Subject         string   `json:"subject,omitempty"`
	Predicate       string   `json:"predicate,omitempty"`
	Object          string   `json:"object,omitempty"`
	Reasons         []string `json:"reasons"`
}

type factPattern struct {
	re        *regexp.Regexp
	predicate string
	family    string
}

var factPatterns = []factPattern{
	{re: regexp.MustCompile(`(?i)^(.+?)\s+default branch is\s+(.+)$`), predicate: "default_branch", family: "config"},
	{re: regexp.MustCompile(`(?i)^(.+?)\s+defaults to\s+(.+)$`), predicate: "defaults_to", family: "config"},
	{re: regexp.MustCompile(`(?i)^(.+?)\s+port is\s+(.+)$`), predicate: "port", family: "config"},
	{re: regexp.MustCompile(`(?i)^(.+?)\s+version is\s+(.+)$`), predicate: "version", family: "config"},
	{re: regexp.MustCompile(`(?i)^(.+?)\s+works at\s+(.+)$`), predicate: "works_at", family: "profile"},
	{re: regexp.MustCompile(`(?i)^(.+?)\s+lives in\s+(.+)$`), predicate: "lives_in", family: "location"},
	{re: regexp.MustCompile(`(?i)^(.+?)\s+located in\s+(.+)$`), predicate: "located_in", family: "location"},
	{re: regexp.MustCompile(`(?i)^(.+?)\s+attended\s+(.+)$`), predicate: "attended", family: "profile"},
	{re: regexp.MustCompile(`(?i)^(.+?)\s+used\s+(.+)$`), predicate: "uses", family: "config"},
	{re: regexp.MustCompile(`(?i)^(.+?)\s+uses\s+(.+)$`), predicate: "uses", family: "config"},
	{re: regexp.MustCompile(`(?i)^(.+?)\s+runs\s+(.+)$`), predicate: "runs", family: "config"},
	{re: regexp.MustCompile(`(?i)^(.+?)\s+is\s+(.+)$`), predicate: "is", family: "identity"},
}

var softClaimMarkers = []string{
	"?", " maybe ", " perhaps ", " probably ", " might ", " could ", " should ", " would ",
	" thinking about ", " considering ", " discuss ", " discussed ", " suggestion ", " suggested ",
	" recommend ", " recommended ", " plan to ", " planning ", " planned ", " explore ", " exploring ",
	" idea ", " follow-up ", " note to self ",
}

var nuanceMarkers = []string{
	" currently ", " current ", " formerly ", " previously ", " used to ", " as of ", " according to ",
	" because ", " although ", " however ", " but ", " while ", " after ", " before ",
}

func AssessFactPromotion(text string) FactPromotionAssessment {
	clean := normalizeFactText(text)
	if clean == "" {
		return FactPromotionAssessment{Confidence: "low", Reasons: []string{"empty text cannot be assessed"}}
	}

	assessment := FactPromotionAssessment{
		KeepAsEntry: true,
		Confidence:  "low",
		Reasons:     []string{},
	}
	lower := " " + strings.ToLower(clean) + " "

	if looksSoftClaim(lower) {
		assessment.Reasons = append(assessment.Reasons,
			"contains planning, suggestion, or uncertainty language",
			"keep this as an entry so the original nuance stays retrievable",
		)
		return assessment
	}
	if hasMultipleClauses(clean) {
		assessment.Reasons = append(assessment.Reasons,
			"contains multiple clauses or sentences rather than one canonical fact",
			"keep this as an entry unless you split out a smaller factual statement",
		)
		return assessment
	}

	subject, predicate, object, family, ok := extractFactCandidate(clean)
	if !ok {
		assessment.Reasons = append(assessment.Reasons,
			"does not fit a stable subject-predicate-object shape",
			"store as an entry unless you can rewrite it as one exact statement",
		)
		return assessment
	}

	assessment.StoreAsFact = true
	assessment.PredicateFamily = family
	assessment.Subject = subject
	assessment.Predicate = predicate
	assessment.Object = object
	assessment.Reasons = append(assessment.Reasons,
		"single-clause statement fits a reusable subject-predicate-object shape",
		"good candidate for exact lookup or timeline queries",
	)

	if keepEntryAlongsideFact(lower, object) {
		assessment.Confidence = "medium"
		assessment.KeepAsEntry = true
		assessment.Reasons = append(assessment.Reasons, "keep the entry too because timing or wording may matter")
		return assessment
	}

	assessment.Confidence = "high"
	assessment.KeepAsEntry = false
	assessment.Reasons = append(assessment.Reasons, "little nuance appears to be lost by storing only the canonical fact")
	return assessment
}

func normalizeFactText(text string) string {
	text = strings.TrimSpace(text)
	text = strings.TrimSuffix(text, ".")
	text = strings.TrimSuffix(text, "!")
	text = strings.TrimSuffix(text, "?")
	return strings.TrimSpace(text)
}

func looksSoftClaim(lower string) bool {
	for _, marker := range softClaimMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func hasMultipleClauses(text string) bool {
	if strings.Contains(text, "\n") || strings.Contains(text, ";") {
		return true
	}
	lower := strings.ToLower(text)
	return strings.Contains(lower, ". ") || strings.Contains(lower, "! ") || strings.Contains(lower, "? ")
}

func extractFactCandidate(text string) (string, string, string, string, bool) {
	for _, pattern := range factPatterns {
		match := pattern.re.FindStringSubmatch(text)
		if len(match) != 3 {
			continue
		}
		subject := strings.TrimSpace(match[1])
		object := strings.TrimSpace(match[2])
		object = strings.TrimSuffix(object, ".")
		object = strings.TrimSuffix(object, "!")
		object = strings.TrimSuffix(object, "?")
		if subject == "" || object == "" {
			continue
		}
		return subject, pattern.predicate, object, pattern.family, true
	}
	return "", "", "", "", false
}

func keepEntryAlongsideFact(lower string, object string) bool {
	for _, marker := range nuanceMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return strings.Contains(object, ",") || strings.Contains(strings.ToLower(object), " and ") || strings.Contains(strings.ToLower(object), " or ")
}
