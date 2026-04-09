package contextview

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/codysnider/tagmem/internal/store"
)

type Options struct {
	IdentityPath string
	Depth        *int
	Tag          string
	Limit        int
}

func Render(entries []store.Entry, options Options) string {
	identity := loadIdentity(options.IdentityPath)
	if options.Limit <= 0 {
		options.Limit = 12
	}
	filtered := make([]store.Entry, 0, len(entries))
	for _, entry := range entries {
		if options.Depth != nil && entry.Depth != *options.Depth {
			continue
		}
		if options.Tag != "" && !containsTag(entry.Tags, options.Tag) {
			continue
		}
		filtered = append(filtered, entry)
	}
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Depth == filtered[j].Depth {
			if filtered[i].UpdatedAt.Equal(filtered[j].UpdatedAt) {
				return filtered[i].ID > filtered[j].ID
			}
			return filtered[i].UpdatedAt.After(filtered[j].UpdatedAt)
		}
		return filtered[i].Depth < filtered[j].Depth
	})
	if len(filtered) > options.Limit {
		filtered = filtered[:options.Limit]
	}
	b := &strings.Builder{}
	b.WriteString("## Identity\n")
	b.WriteString(identity)
	b.WriteString("\n\n## Current Context\n")
	if len(filtered) == 0 {
		b.WriteString("No entries available.\n")
		return b.String()
	}
	for _, entry := range filtered {
		line := fmt.Sprintf("- [depth %d] %s", entry.Depth, entry.Title)
		if len(entry.Tags) > 0 {
			line += "  tags=" + strings.Join(entry.Tags, ",")
		}
		b.WriteString(line + "\n")
		snippet := strings.ReplaceAll(strings.TrimSpace(entry.Body), "\n", " ")
		if len(snippet) > 220 {
			snippet = snippet[:217] + "..."
		}
		b.WriteString("  " + snippet + "\n")
	}
	return b.String()
}

func loadIdentity(path string) string {
	if path == "" {
		return "No identity configured."
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "No identity configured."
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return "No identity configured."
	}
	return text
}

func containsTag(tags []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, tag := range tags {
		if strings.ToLower(strings.TrimSpace(tag)) == target {
			return true
		}
	}
	return false
}
