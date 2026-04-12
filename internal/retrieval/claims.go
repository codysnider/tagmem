package retrieval

import (
	"regexp"
	"sort"
	"strings"
)

type ClaimFeatures struct {
	Entities     []string
	Environment  string
	Speaker      string
	Values       []string
	ValueKinds   []string
	Keywords     []string
	Intent       string
	State        string
	Assertion    string
	Approximate  bool
	Precision    float64
	ExactWanted  bool
	HasAssistant bool
	HasUser      bool
}

var (
	titleEntityPattern = regexp.MustCompile(`\b[A-Z][a-z0-9]+(?:\s+[A-Z][a-z0-9]+){0,2}\b`)
	hostPattern        = regexp.MustCompile(`\b[a-z0-9.-]+\.[a-z]{2,}\b`)
	numberPattern      = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:\s?(?:seconds?|minutes?|hours?|days?|mb|gb|tb|utc|am|pm))?\b`)
	moneyPattern       = regexp.MustCompile(`\$\s?\d+(?:\.\d+)?`)
	isoDatePattern     = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)
	monthPattern       = regexp.MustCompile(`\b(january|february|march|april|may|june|july|august|september|october|november|december)\b`)
	quarterPattern     = regexp.MustCompile(`\bq[1-4]\b|\bquarter\s+[1-4]\b`)
	versionPattern     = regexp.MustCompile(`\bv?\d+(?:\.\d+){1,3}\b`)
	quotedPattern      = regexp.MustCompile(`"([^"]{2,80})"|'([^']{2,80})'`)
)

var approxMarkers = []string{"about", "roughly", "approximately", "around", "almost", "nearly", "close to", "more than", "less than", "under", "over", "q1", "q2", "q3", "q4", "early ", "mid ", "late ", "recently", "soon"}

func ExtractClaimFeatures(text string) ClaimFeatures {
	clean := strings.TrimSpace(text)
	lower := strings.ToLower(clean)
	entities := uniqueNormalized(titleEntityPattern.FindAllString(clean, -1))
	values := make([]string, 0)
	values = append(values, normalizeValueMatches(hostPattern.FindAllString(lower, -1))...)
	values = append(values, normalizeValueMatches(numberPattern.FindAllString(lower, -1))...)
	values = append(values, normalizeValueMatches(moneyPattern.FindAllString(lower, -1))...)
	values = append(values, normalizeValueMatches(isoDatePattern.FindAllString(lower, -1))...)
	values = append(values, normalizeValueMatches(monthPattern.FindAllString(lower, -1))...)
	values = append(values, normalizeValueMatches(quarterPattern.FindAllString(lower, -1))...)
	values = append(values, normalizeValueMatches(versionPattern.FindAllString(lower, -1))...)
	for _, match := range quotedPattern.FindAllStringSubmatch(clean, -1) {
		for _, part := range match[1:] {
			part = strings.TrimSpace(strings.ToLower(part))
			if part != "" {
				values = append(values, part)
			}
		}
	}
	keywords := ExtractKeywords(clean)
	sort.Strings(keywords)
	valueKinds := detectValueKinds(lower)
	approximate := detectApproximate(lower)
	precision := detectPrecision(lower, valueKinds, approximate)
	exactWanted := detectExactWanted(lower)
	return ClaimFeatures{
		Entities:     entities,
		Environment:  detectEnvironment(lower),
		Speaker:      detectSpeaker(lower),
		Values:       uniqueNormalized(values),
		ValueKinds:   valueKinds,
		Keywords:     keywords,
		Intent:       detectIntent(lower),
		State:        detectState(lower),
		Assertion:    detectAssertion(lower),
		Approximate:  approximate,
		Precision:    precision,
		ExactWanted:  exactWanted,
		HasAssistant: strings.Contains(lower, "you ") || strings.Contains(lower, "assistant"),
		HasUser:      strings.Contains(lower, "i ") || strings.Contains(lower, "user"),
	}
}

func detectIntent(lower string) string {
	switch {
	case strings.Contains(lower, "what did you suggest") || strings.Contains(lower, "what did you recommend") || strings.Contains(lower, "you suggested"):
		return "suggestion"
	case strings.Contains(lower, "what does") && strings.Contains(lower, "prefer"):
		return "preference"
	case strings.HasPrefix(lower, "when did") || strings.Contains(lower, " when did "):
		return "temporal-event"
	case strings.Contains(lower, "current ") || strings.Contains(lower, "currently ") || strings.HasPrefix(lower, "what is the current"):
		return "current-state"
	case strings.HasPrefix(lower, "what is ") || strings.HasPrefix(lower, "what does ") || strings.HasPrefix(lower, "which "):
		return "value-lookup"
	default:
		return ""
	}
}

func detectState(lower string) string {
	switch {
	case strings.Contains(lower, "used to") || strings.Contains(lower, "formerly") || strings.Contains(lower, "previously") || strings.Contains(lower, "was "):
		return "historical"
	case strings.Contains(lower, "plan to") || strings.Contains(lower, "planning") || strings.Contains(lower, "planned") || strings.Contains(lower, "discussed"):
		return "planned"
	case strings.Contains(lower, "current") || strings.Contains(lower, "currently") || strings.Contains(lower, "is ") || strings.Contains(lower, "uses ") || strings.Contains(lower, "runs ") || strings.Contains(lower, "defaults to") || strings.Contains(lower, "prefers") || strings.Contains(lower, "suggested"):
		return "asserted"
	default:
		return ""
	}
}

func detectAssertion(lower string) string {
	switch {
	case strings.Contains(lower, "defaults to"):
		return "defaults"
	case strings.Contains(lower, "uses "):
		return "uses"
	case strings.Contains(lower, "runs "):
		return "runs"
	case strings.Contains(lower, "prefers") || strings.Contains(lower, "favorite"):
		return "preference"
	case strings.Contains(lower, "suggested") || strings.Contains(lower, "recommended"):
		return "suggestion"
	case strings.Contains(lower, "migrated") || strings.Contains(lower, "shipped") || strings.Contains(lower, "published") || strings.Contains(lower, "rotated") || strings.Contains(lower, "switched"):
		return "event"
	default:
		return ""
	}
}

func detectApproximate(lower string) bool {
	for _, marker := range approxMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func detectExactWanted(lower string) bool {
	switch {
	case strings.HasPrefix(lower, "when did"):
		return true
	case strings.Contains(lower, "how much") || strings.Contains(lower, "how many") || strings.Contains(lower, "how long"):
		return true
	case strings.Contains(lower, "what port") || strings.Contains(lower, "what version") || strings.Contains(lower, "what domain") || strings.Contains(lower, "what timeout"):
		return true
	case strings.HasPrefix(lower, "what is") || strings.HasPrefix(lower, "what does") || strings.HasPrefix(lower, "which "):
		return true
	default:
		return false
	}
}

func detectValueKinds(lower string) []string {
	kinds := []string{}
	if len(moneyPattern.FindAllString(lower, -1)) > 0 {
		kinds = append(kinds, "money")
	}
	if len(isoDatePattern.FindAllString(lower, -1)) > 0 || len(monthPattern.FindAllString(lower, -1)) > 0 || len(quarterPattern.FindAllString(lower, -1)) > 0 {
		kinds = append(kinds, "date")
	}
	if strings.Contains(lower, "second") || strings.Contains(lower, "minute") || strings.Contains(lower, "hour") || strings.Contains(lower, "day") {
		kinds = append(kinds, "duration")
	}
	if len(hostPattern.FindAllString(lower, -1)) > 0 {
		kinds = append(kinds, "domain")
	}
	if len(versionPattern.FindAllString(lower, -1)) > 0 {
		kinds = append(kinds, "version")
	}
	if strings.Contains(lower, "port") {
		kinds = append(kinds, "port")
	}
	if len(numberPattern.FindAllString(lower, -1)) > 0 {
		kinds = append(kinds, "quantity")
	}
	return uniqueNormalized(kinds)
}

func detectPrecision(lower string, valueKinds []string, approximate bool) float64 {
	precision := 0.35
	if len(valueKinds) > 0 {
		precision = 0.65
	}
	if len(isoDatePattern.FindAllString(lower, -1)) > 0 {
		precision = 1.0
	} else if len(moneyPattern.FindAllString(lower, -1)) > 0 {
		precision = 0.95
	} else if len(versionPattern.FindAllString(lower, -1)) > 0 {
		precision = maxFloat(precision, 0.90)
	} else if len(hostPattern.FindAllString(lower, -1)) > 0 {
		precision = maxFloat(precision, 0.90)
	} else if len(numberPattern.FindAllString(lower, -1)) > 0 {
		precision = maxFloat(precision, 0.80)
	} else if len(monthPattern.FindAllString(lower, -1)) > 0 {
		precision = maxFloat(precision, 0.60)
	} else if len(quarterPattern.FindAllString(lower, -1)) > 0 {
		precision = maxFloat(precision, 0.50)
	}
	if approximate {
		precision *= 0.7
	}
	if precision < 0.10 {
		return 0.10
	}
	if precision > 1.0 {
		return 1.0
	}
	return precision
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func detectEnvironment(lower string) string {
	for _, env := range []string{"production", "prod", "staging", "stage", "development", "dev", "preview", "test"} {
		if strings.Contains(lower, env) {
			return env
		}
	}
	return ""
}

func detectSpeaker(lower string) string {
	switch {
	case strings.Contains(lower, "you suggested") || strings.Contains(lower, "you said") || strings.Contains(lower, "assistant"):
		return "assistant"
	case strings.Contains(lower, "i suggested") || strings.Contains(lower, "i said") || strings.Contains(lower, "user"):
		return "user"
	default:
		return ""
	}
}

func normalizeValueMatches(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func uniqueNormalized(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func EntityOverlap(a, b ClaimFeatures) float64 {
	return setOverlap(a.Entities, b.Entities)
}

func ValueOverlap(a, b ClaimFeatures) float64 {
	return setOverlap(a.Values, b.Values)
}

func KeywordSetOverlap(a, b ClaimFeatures) float64 {
	return setOverlap(a.Keywords, b.Keywords)
}

func setOverlap(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := map[string]struct{}{}
	for _, item := range a {
		set[item] = struct{}{}
	}
	hits := 0
	for _, item := range b {
		if _, ok := set[item]; ok {
			hits++
		}
	}
	denom := len(a)
	if len(b) < denom {
		denom = len(b)
	}
	if denom == 0 {
		return 0
	}
	return float64(hits) / float64(denom)
}
