package importer

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/codysnider/tagmem/internal/store"
	"github.com/codysnider/tagmem/internal/tagging"
	"github.com/codysnider/tagmem/internal/vector"
)

var readableExtensions = map[string]struct{}{
	".txt": {}, ".md": {}, ".py": {}, ".js": {}, ".ts": {}, ".jsx": {}, ".tsx": {},
	".json": {}, ".jsonl": {}, ".yaml": {}, ".yml": {}, ".html": {}, ".css": {},
	".java": {}, ".go": {}, ".rs": {}, ".rb": {}, ".sh": {}, ".csv": {}, ".sql": {}, ".toml": {},
}

var skipDirs = map[string]struct{}{
	".git": {}, "node_modules": {}, "__pycache__": {}, ".venv": {}, "venv": {}, "env": {},
	"dist": {}, "build": {}, ".next": {}, "coverage": {}, ".cache": {}, ".idea": {}, ".vscode": {},
	"target": {}, ".mempalace": {}, ".opencode": {},
}

var skipFilenames = map[string]struct{}{
	".gitignore":        {},
	"package-lock.json": {},
	"mempalace.yaml":    {},
	"mempalace.yml":     {},
	"mempal.yaml":       {},
	"mempal.yml":        {},
}

const chunkSize = 800
const chunkOverlap = 100
const minChunkSize = 50

type Mode string

const (
	ModeFiles         Mode = "files"
	ModeConversations Mode = "conversations"
)

type Options struct {
	SourceDir        string
	Mode             Mode
	Extract          string
	Depth            int
	Limit            int
	DryRun           bool
	SkipExisting     bool
	RespectGitignore bool
	IncludeIgnored   []string
	Provider         *vector.Provider
}

type Result struct {
	FilesProcessed int            `json:"files_processed"`
	FilesSkipped   int            `json:"files_skipped"`
	EntriesAdded   int            `json:"entries_added"`
	Tags           map[string]int `json:"tags"`
}

func Run(repo *store.Repository, options Options) (Result, error) {
	root, err := filepath.Abs(options.SourceDir)
	if err != nil {
		return Result{}, err
	}
	seenSources := map[string]struct{}{}
	if options.SkipExisting {
		existing, err := repo.List(store.Query{Limit: 0})
		if err != nil {
			return Result{}, err
		}
		for _, entry := range existing {
			if entry.Origin != "" {
				seenSources[entry.Origin] = struct{}{}
			}
		}
	}
	result := Result{Tags: map[string]int{}}
	includeIgnored := normalizeIncludePaths(options.IncludeIgnored)
	hasGitignore := options.RespectGitignore && hasGitRepo(root)
	err = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if _, ok := skipDirs[d.Name()]; ok && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if options.Limit > 0 && result.FilesProcessed >= options.Limit {
			return stopWalk
		}
		if _, ok := skipFilenames[strings.ToLower(d.Name())]; ok {
			return nil
		}
		if options.Mode == ModeConversations && strings.HasSuffix(strings.ToLower(d.Name()), ".meta.json") {
			return nil
		}
		if _, ok := readableExtensions[strings.ToLower(filepath.Ext(path))]; !ok {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if hasGitignore && !isIncluded(rel, includeIgnored) && gitIgnored(root, rel) {
			result.FilesSkipped++
			return nil
		}
		if _, ok := seenSources[rel]; ok {
			result.FilesSkipped++
			return nil
		}
		content, err := loadContent(path, options.Mode)
		if err != nil || strings.TrimSpace(content) == "" {
			result.FilesSkipped++
			return nil
		}
		chunks := chunkContent(content, options.Mode, options.Extract)
		if len(chunks) == 0 {
			result.FilesSkipped++
			return nil
		}
		tags := tagging.BuildTags(rel, content, tagging.Mode(options.Mode), options.Provider)
		result.FilesProcessed++
		for _, tag := range tags {
			result.Tags[tag]++
		}
		if options.DryRun {
			result.EntriesAdded += len(chunks)
			return nil
		}
		batch := make([]store.AddEntry, 0, len(chunks))
		for index, chunk := range chunks {
			batch = append(batch, store.AddEntry{Depth: options.Depth, Title: makeTitle(rel, index, len(chunks)), Body: chunk, Tags: tags, Source: content, Origin: rel})
		}
		added, err := repo.AddMany(batch)
		if err != nil {
			return err
		}
		result.EntriesAdded += len(added)
		return nil
	})
	if err != nil && err != stopWalk {
		return Result{}, err
	}
	return result, nil
}

var stopWalk = fmt.Errorf("stop walk")

func loadContent(path string, mode Mode) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	text := string(data)
	if mode == ModeConversations {
		return normalizeConversation(text), nil
	}
	if strings.HasSuffix(strings.ToLower(path), ".json") || strings.HasSuffix(strings.ToLower(path), ".jsonl") {
		if normalized := flattenJSONText(data); normalized != "" {
			return normalized, nil
		}
	}
	return text, nil
}

func flattenJSONText(data []byte) string {
	var value interface{}
	if err := json.Unmarshal(data, &value); err != nil {
		return ""
	}
	parts := []string{}
	collectJSONText(value, &parts)
	return strings.Join(parts, "\n")
}

func collectJSONText(value interface{}, parts *[]string) {
	switch v := value.(type) {
	case map[string]interface{}:
		for key, item := range v {
			lower := strings.ToLower(key)
			if lower == "text" || lower == "content" || lower == "message" || lower == "prompt" || lower == "response" || lower == "body" {
				if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
					*parts = append(*parts, text)
					continue
				}
			}
			collectJSONText(item, parts)
		}
	case []interface{}:
		for _, item := range v {
			collectJSONText(item, parts)
		}
	}
}

func normalizeConversation(content string) string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	return content
}

func chunkContent(content string, mode Mode, extract string) []string {
	if mode == ModeConversations {
		if strings.ToLower(strings.TrimSpace(extract)) == "general" {
			return extractGeneralConversationMemories(content)
		}
		return chunkConversation(content, extract)
	}
	return chunkGeneric(content)
}

func chunkGeneric(content string) []string {
	content = strings.TrimSpace(content)
	if len(content) <= chunkSize {
		if len(content) < minChunkSize {
			return nil
		}
		return []string{content}
	}
	chunks := []string{}
	for start := 0; start < len(content); {
		end := min(start+chunkSize, len(content))
		if end < len(content) {
			if split := strings.LastIndex(content[start:end], "\n"); split > chunkSize/2 {
				end = start + split
			}
		}
		chunk := strings.TrimSpace(content[start:end])
		if len(chunk) >= minChunkSize {
			chunks = append(chunks, chunk)
		}
		if end >= len(content) {
			break
		}
		start = max(end-chunkOverlap, start+1)
	}
	return chunks
}

func chunkConversation(content string, extract string) []string {
	lines := strings.Split(content, "\n")
	quoted := 0
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), ">") {
			quoted++
		}
	}
	if quoted >= 3 {
		chunks := []string{}
		for i := 0; i < len(lines); {
			if !strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
				i++
				continue
			}
			user := strings.TrimSpace(lines[i])
			i++
			resp := []string{}
			for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), ">") {
				if strings.TrimSpace(lines[i]) != "" {
					resp = append(resp, strings.TrimSpace(lines[i]))
				}
				i++
			}
			chunk := strings.TrimSpace(user + "\n" + strings.Join(resp, " "))
			if len(chunk) >= minChunkSize {
				chunks = append(chunks, chunk)
			}
		}
		if len(chunks) > 0 {
			return chunks
		}
	}
	paragraphs := strings.Split(content, "\n\n")
	chunks := []string{}
	for _, paragraph := range paragraphs {
		paragraph = strings.TrimSpace(paragraph)
		if len(paragraph) >= minChunkSize {
			chunks = append(chunks, paragraph)
		}
	}
	if len(chunks) > 0 {
		return chunks
	}
	return chunkGeneric(content)
}

func extractGeneralConversationMemories(content string) []string {
	segments := splitConversationSegments(content)
	type rule struct {
		tag      string
		keywords []string
		minScore int
	}
	rules := []rule{
		{tag: "decisions", keywords: []string{"decided", "chose", "picked", "switched", "migrated", "replace", "approach"}, minScore: 1},
		{tag: "preferences", keywords: []string{"i like", "i love", "i prefer", "favorite", "i enjoy", "i dislike"}, minScore: 1},
		{tag: "milestones", keywords: []string{"finished", "completed", "launched", "shipped", "graduated", "started", "joined"}, minScore: 1},
		{tag: "problems", keywords: []string{"problem", "issue", "broken", "failed", "stuck", "error", "fix"}, minScore: 1},
		{tag: "emotional", keywords: []string{"worried", "excited", "happy", "frustrated", "stressed", "powerful", "upset"}, minScore: 1},
	}
	chunks := make([]string, 0)
	seen := map[string]struct{}{}
	for _, segment := range segments {
		lower := strings.ToLower(segment)
		bestTag := ""
		bestScore := 0
		for _, r := range rules {
			score := 0
			for _, keyword := range r.keywords {
				if strings.Contains(lower, keyword) {
					score++
				}
			}
			if score >= r.minScore && score > bestScore {
				bestTag = r.tag
				bestScore = score
			}
		}
		if bestTag == "" {
			continue
		}
		memory := strings.TrimSpace("[" + bestTag + "] " + segment)
		if len(memory) < minChunkSize {
			continue
		}
		if _, ok := seen[memory]; ok {
			continue
		}
		seen[memory] = struct{}{}
		chunks = append(chunks, memory)
	}
	if len(chunks) > 0 {
		return chunks
	}
	return chunkConversation(content, "exchange")
}

func splitConversationSegments(content string) []string {
	lines := strings.Split(content, "\n")
	segments := []string{}
	for i := 0; i < len(lines); {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, ">") {
			user := line
			i++
			resp := []string{}
			for i < len(lines) {
				next := strings.TrimSpace(lines[i])
				if strings.HasPrefix(next, ">") || strings.HasPrefix(next, "---") {
					break
				}
				if next != "" {
					resp = append(resp, next)
				}
				i++
			}
			segment := strings.TrimSpace(user + "\n" + strings.Join(resp, " "))
			if segment != "" {
				segments = append(segments, segment)
			}
			continue
		}
		if line != "" {
			segments = append(segments, line)
		}
		i++
	}
	return segments
}

func makeTitle(rel string, index, total int) string {
	if total <= 1 {
		return rel
	}
	return fmt.Sprintf("%s#%d", rel, index+1)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func hasGitRepo(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

func gitIgnored(root, rel string) bool {
	cmd := exec.Command("git", "check-ignore", "-q", "--", rel)
	cmd.Dir = root
	err := cmd.Run()
	return err == nil
}

func normalizeIncludePaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		for _, part := range strings.Split(path, ",") {
			part = filepath.ToSlash(strings.Trim(strings.TrimSpace(part), "/"))
			if part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func isIncluded(rel string, includes []string) bool {
	for _, include := range includes {
		if rel == include || strings.HasPrefix(rel, include+"/") || strings.HasPrefix(include, rel+"/") {
			return true
		}
	}
	return false
}
