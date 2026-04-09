package diary

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Entry struct {
	ID        string `json:"id"`
	Agent     string `json:"agent"`
	Topic     string `json:"topic"`
	Content   string `json:"content"`
	Timestamp string `json:"timestamp"`
	Date      string `json:"date"`
}

type Store struct {
	dir string
	now func() time.Time
}

func New(dir string) *Store {
	return &Store{dir: dir, now: time.Now}
}

func (s *Store) Write(agent, content, topic string) (Entry, error) {
	agent = normalizeAgent(agent)
	if topic == "" {
		topic = "general"
	}
	now := s.now().UTC()
	entry := Entry{ID: fmt.Sprintf("diary_%s_%s", agent, now.Format("20060102_150405")), Agent: agent, Topic: topic, Content: content, Timestamp: now.Format(time.RFC3339), Date: now.Format("2006-01-02")}
	entries, err := s.load(agent)
	if err != nil {
		return Entry{}, err
	}
	entries = append(entries, entry)
	return entry, s.save(agent, entries)
}

func (s *Store) Read(agent string, lastN int) ([]Entry, error) {
	entries, err := s.load(normalizeAgent(agent))
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Timestamp > entries[j].Timestamp })
	if lastN > 0 && len(entries) > lastN {
		entries = entries[:lastN]
	}
	return entries, nil
}

func (s *Store) load(agent string) ([]Entry, error) {
	path := filepath.Join(s.dir, agent+".json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return []Entry{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func (s *Store) save(agent string, entries []Entry) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(s.dir, agent+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func normalizeAgent(agent string) string {
	agent = strings.ToLower(strings.TrimSpace(agent))
	agent = strings.ReplaceAll(agent, " ", "-")
	if agent == "" {
		return "default"
	}
	return agent
}
