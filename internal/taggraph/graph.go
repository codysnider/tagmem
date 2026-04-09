package taggraph

import (
	"sort"
	"strings"

	"github.com/codysnider/tagmem/internal/store"
)

type Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

func TagCounts(entries []store.Entry) map[string]int {
	counts := map[string]int{}
	for _, entry := range entries {
		for _, tag := range entry.Tags {
			counts[tag]++
		}
	}
	return counts
}

func DepthTagMap(entries []store.Entry) map[int]map[string]int {
	mapping := map[int]map[string]int{}
	for _, entry := range entries {
		if _, ok := mapping[entry.Depth]; !ok {
			mapping[entry.Depth] = map[string]int{}
		}
		for _, tag := range entry.Tags {
			mapping[entry.Depth][tag]++
		}
	}
	return mapping
}

func Traverse(entries []store.Entry, startTag string, maxHops int) []map[string]any {
	adj := buildAdjacency(entries)
	startTag = normalize(startTag)
	if maxHops <= 0 {
		maxHops = 2
	}
	type node struct {
		tag  string
		hops int
	}
	queue := []node{{tag: startTag, hops: 0}}
	seen := map[string]bool{startTag: true}
	results := []map[string]any{}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		if current.hops >= maxHops {
			continue
		}
		neighbors := adjacencyList(adj[current.tag])
		for _, neighbor := range neighbors {
			results = append(results, map[string]any{"from": current.tag, "to": neighbor.tag, "hops": current.hops + 1, "count": neighbor.count})
			if seen[neighbor.tag] {
				continue
			}
			seen[neighbor.tag] = true
			queue = append(queue, node{tag: neighbor.tag, hops: current.hops + 1})
		}
	}
	return results
}

func FindBridges(entries []store.Entry, depthA, depthB *int) []map[string]any {
	mapping := DepthTagMap(entries)
	results := []map[string]any{}
	if depthA != nil && depthB != nil {
		for tag, countA := range mapping[*depthA] {
			if countB, ok := mapping[*depthB][tag]; ok {
				results = append(results, map[string]any{"tag": tag, "depth_a": *depthA, "depth_b": *depthB, "count_a": countA, "count_b": countB})
			}
		}
		return results
	}
	seen := map[string]map[int]int{}
	for depth, tags := range mapping {
		for tag, count := range tags {
			if _, ok := seen[tag]; !ok {
				seen[tag] = map[int]int{}
			}
			seen[tag][depth] = count
		}
	}
	for tag, depths := range seen {
		if len(depths) < 2 {
			continue
		}
		results = append(results, map[string]any{"tag": tag, "depths": depths})
	}
	return results
}

func Stats(entries []store.Entry) map[string]any {
	adj := buildAdjacency(entries)
	edgeCount := 0
	for from, neighbors := range adj {
		for to := range neighbors {
			if from < to {
				edgeCount++
			}
		}
	}
	tagCounts := TagCounts(entries)
	return map[string]any{"tags": len(tagCounts), "edges": edgeCount, "entry_count": len(entries)}
}

type neighbor struct {
	tag   string
	count int
}

func buildAdjacency(entries []store.Entry) map[string]map[string]int {
	adj := map[string]map[string]int{}
	for _, entry := range entries {
		tags := normalizeTags(entry.Tags)
		for i := 0; i < len(tags); i++ {
			for j := i + 1; j < len(tags); j++ {
				link(adj, tags[i], tags[j])
				link(adj, tags[j], tags[i])
			}
		}
	}
	return adj
}

func link(adj map[string]map[string]int, from, to string) {
	if _, ok := adj[from]; !ok {
		adj[from] = map[string]int{}
	}
	adj[from][to]++
}

func adjacencyList(raw map[string]int) []neighbor {
	neighbors := make([]neighbor, 0, len(raw))
	for tag, count := range raw {
		neighbors = append(neighbors, neighbor{tag: tag, count: count})
	}
	sort.Slice(neighbors, func(i, j int) bool {
		if neighbors[i].count == neighbors[j].count {
			return neighbors[i].tag < neighbors[j].tag
		}
		return neighbors[i].count > neighbors[j].count
	})
	return neighbors
}

func normalizeTags(tags []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = normalize(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
