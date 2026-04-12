package kg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Fact struct {
	ID        int    `json:"id"`
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`
	ValidFrom string `json:"valid_from,omitempty"`
	ValidTo   string `json:"valid_to,omitempty"`
	Source    string `json:"source,omitempty"`
}

type Snapshot struct {
	NextID int    `json:"next_id"`
	Facts  []Fact `json:"facts"`
}

type Store struct {
	path string
	now  func() time.Time
}

func New(path string) *Store {
	return &Store{path: path, now: time.Now}
}

func (s *Store) Add(subject, predicate, object, validFrom, source string) (Fact, error) {
	snapshot, err := s.load()
	if err != nil {
		return Fact{}, err
	}
	fact := Fact{ID: snapshot.NextID, Subject: strings.TrimSpace(subject), Predicate: strings.TrimSpace(predicate), Object: strings.TrimSpace(object), ValidFrom: strings.TrimSpace(validFrom), Source: strings.TrimSpace(source)}
	if fact.ValidFrom == "" {
		fact.ValidFrom = s.now().UTC().Format("2006-01-02")
	}
	snapshot.NextID++
	snapshot.Facts = append(snapshot.Facts, fact)
	return fact, s.save(snapshot)
}

func (s *Store) Invalidate(subject, predicate, object, ended string) error {
	subject = strings.TrimSpace(subject)
	predicate = strings.TrimSpace(predicate)
	object = strings.TrimSpace(object)
	snapshot, err := s.load()
	if err != nil {
		return err
	}
	if strings.TrimSpace(ended) == "" {
		ended = s.now().UTC().Format("2006-01-02")
	}
	for i := range snapshot.Facts {
		fact := &snapshot.Facts[i]
		if fact.Subject == subject && fact.Predicate == predicate && fact.Object == object && fact.ValidTo == "" {
			fact.ValidTo = ended
		}
	}
	return s.save(snapshot)
}

func (s *Store) Query(entity, asOf, direction string) ([]Fact, error) {
	entity = strings.TrimSpace(entity)
	asOf = strings.TrimSpace(asOf)
	snapshot, err := s.load()
	if err != nil {
		return nil, err
	}
	results := make([]Fact, 0)
	for _, fact := range snapshot.Facts {
		if !matchesDirection(fact, entity, direction) {
			continue
		}
		if !factValidAt(fact, asOf) {
			continue
		}
		results = append(results, fact)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].ValidFrom == results[j].ValidFrom {
			return results[i].ID < results[j].ID
		}
		return results[i].ValidFrom < results[j].ValidFrom
	})
	return results, nil
}

func (s *Store) Timeline(entity string) ([]Fact, error) {
	entity = strings.TrimSpace(entity)
	snapshot, err := s.load()
	if err != nil {
		return nil, err
	}
	results := make([]Fact, 0)
	for _, fact := range snapshot.Facts {
		if entity != "" && fact.Subject != entity && fact.Object != entity {
			continue
		}
		results = append(results, fact)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].ValidFrom == results[j].ValidFrom {
			return results[i].ID < results[j].ID
		}
		return results[i].ValidFrom < results[j].ValidFrom
	})
	return results, nil
}

func (s *Store) Stats() (map[string]any, error) {
	snapshot, err := s.load()
	if err != nil {
		return nil, err
	}
	entities := map[string]struct{}{}
	predicates := map[string]int{}
	current := 0
	expired := 0
	for _, fact := range snapshot.Facts {
		entities[fact.Subject] = struct{}{}
		entities[fact.Object] = struct{}{}
		predicates[fact.Predicate]++
		if fact.ValidTo == "" {
			current++
		} else {
			expired++
		}
	}
	return map[string]any{"entities": len(entities), "facts": len(snapshot.Facts), "current": current, "expired": expired, "predicates": predicates}, nil
}

func (s *Store) load() (Snapshot, error) {
	if _, err := os.Stat(s.path); os.IsNotExist(err) {
		return Snapshot{NextID: 1, Facts: []Fact{}}, nil
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return Snapshot{}, err
	}
	var snapshot Snapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return Snapshot{}, err
	}
	if snapshot.NextID == 0 {
		snapshot.NextID = len(snapshot.Facts) + 1
	}
	if snapshot.Facts == nil {
		snapshot.Facts = []Fact{}
	}
	return snapshot, nil
}

func (s *Store) save(snapshot Snapshot) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func matchesDirection(fact Fact, entity, direction string) bool {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "outgoing":
		return fact.Subject == entity
	case "incoming":
		return fact.Object == entity
	default:
		return fact.Subject == entity || fact.Object == entity
	}
}

func factValidAt(fact Fact, asOf string) bool {
	asOf = strings.TrimSpace(asOf)
	if asOf == "" {
		return fact.ValidTo == ""
	}
	if fact.ValidFrom != "" && fact.ValidFrom > asOf {
		return false
	}
	if fact.ValidTo != "" && fact.ValidTo < asOf {
		return false
	}
	return true
}
