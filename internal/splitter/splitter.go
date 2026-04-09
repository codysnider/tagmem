package splitter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Result struct {
	FilesScanned int      `json:"files_scanned"`
	MegaFiles    int      `json:"mega_files"`
	Sessions     int      `json:"sessions"`
	Outputs      []string `json:"outputs"`
}

var tsPattern = regexp.MustCompile(`⏺\s+(\d{1,2}:\d{2}\s+[AP]M)\s+\w+,\s+(\w+)\s+(\d{1,2}),\s+(\d{4})`)
var skipPromptPattern = regexp.MustCompile(`^(\./|cd |ls |python|bash|git |cat |source |export |claude|./activate)`)

func Run(sourceDir, outputDir string, minSessions int, dryRun bool) (Result, error) {
	files, err := filepath.Glob(filepath.Join(sourceDir, "*.txt"))
	if err != nil {
		return Result{}, err
	}
	result := Result{FilesScanned: len(files)}
	for _, file := range files {
		lines, err := readLines(file)
		if err != nil {
			return Result{}, err
		}
		boundaries := findSessionBoundaries(lines)
		if len(boundaries) < minSessions {
			continue
		}
		result.MegaFiles++
		written, err := splitFile(file, lines, outputDir, dryRun)
		if err != nil {
			return Result{}, err
		}
		result.Sessions += len(written)
		result.Outputs = append(result.Outputs, written...)
	}
	return result, nil
}

func splitFile(path string, lines []string, outputDir string, dryRun bool) ([]string, error) {
	boundaries := findSessionBoundaries(lines)
	if len(boundaries) < 2 {
		return nil, nil
	}
	boundaries = append(boundaries, len(lines))
	outDir := outputDir
	if outDir == "" {
		outDir = filepath.Dir(path)
	}
	outputs := []string{}
	for i := 0; i < len(boundaries)-1; i++ {
		chunk := lines[boundaries[i]:boundaries[i+1]]
		if len(chunk) < 10 {
			continue
		}
		name := buildFilename(path, chunk, i)
		outPath := filepath.Join(outDir, name)
		outputs = append(outputs, outPath)
		if dryRun {
			continue
		}
		if err := os.MkdirAll(outDir, 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(outPath, []byte(strings.Join(chunk, "")), 0o644); err != nil {
			return nil, err
		}
	}
	if !dryRun && len(outputs) > 0 {
		backup := strings.TrimSuffix(path, filepath.Ext(path)) + ".mega_backup"
		if err := os.Rename(path, backup); err != nil {
			return nil, err
		}
	}
	return outputs, nil
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	parts := strings.SplitAfter(text, "\n")
	if len(parts) == 1 && parts[0] == "" {
		return nil, nil
	}
	return parts, nil
}

func findSessionBoundaries(lines []string) []int {
	boundaries := []int{}
	for i, line := range lines {
		if strings.Contains(line, "Claude Code v") && isTrueSessionStart(lines, i) {
			boundaries = append(boundaries, i)
		}
	}
	return boundaries
}

func isTrueSessionStart(lines []string, idx int) bool {
	end := idx + 6
	if end > len(lines) {
		end = len(lines)
	}
	nearby := strings.Join(lines[idx:end], "")
	return !strings.Contains(nearby, "Ctrl+E") && !strings.Contains(nearby, "previous messages")
}

func buildFilename(source string, chunk []string, index int) string {
	stamp := extractTimestamp(chunk)
	people := extractPeople(chunk)
	subject := extractSubject(chunk)
	sourceStem := sanitize(strings.TrimSuffix(filepath.Base(source), filepath.Ext(source)))
	peoplePart := "unknown"
	if len(people) > 0 {
		peoplePart = sanitize(strings.Join(people, "-"))
	}
	return fmt.Sprintf("%s__%s_%s_%s.txt", sourceStem, stampOrPart(stamp, index), peoplePart, sanitize(subject))
}

func extractTimestamp(lines []string) string {
	months := map[string]string{"January": "01", "February": "02", "March": "03", "April": "04", "May": "05", "June": "06", "July": "07", "August": "08", "September": "09", "October": "10", "November": "11", "December": "12"}
	for i, line := range lines {
		if i >= 50 {
			break
		}
		match := tsPattern.FindStringSubmatch(line)
		if len(match) == 5 {
			timePart := strings.ReplaceAll(strings.ReplaceAll(match[1], ":", ""), " ", "")
			return fmt.Sprintf("%s-%s-%02s_%s", match[4], months[match[2]], match[3], timePart)
		}
	}
	return ""
}

func extractPeople(lines []string) []string {
	known := []string{"Alice", "Ben", "Riley", "Max", "Sam", "Devon", "Jordan"}
	text := strings.Join(lines, "")
	found := []string{}
	for _, person := range known {
		if strings.Contains(strings.ToLower(text), strings.ToLower(person)) {
			found = append(found, person)
		}
	}
	return found
}

func extractSubject(lines []string) string {
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "> ") {
			continue
		}
		prompt := strings.TrimSpace(strings.TrimPrefix(line, "> "))
		if prompt == "" || skipPromptPattern.MatchString(prompt) || len(prompt) <= 5 {
			continue
		}
		return prompt
	}
	return "session"
}

func sanitize(value string) string {
	value = strings.TrimSpace(value)
	value = regexp.MustCompile(`[^\w\.-]+`).ReplaceAllString(value, "_")
	value = regexp.MustCompile(`_+`).ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if len(value) > 60 {
		value = value[:60]
	}
	if value == "" {
		return "session"
	}
	return value
}

func stampOrPart(stamp string, index int) string {
	if stamp != "" {
		return stamp
	}
	return fmt.Sprintf("part%02d", index+1)
}
