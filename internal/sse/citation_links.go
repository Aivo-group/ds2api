package sse

import (
	"sort"
	"strconv"
	"strings"
)

type CitationSource struct {
	Index       int     `json:"index"`
	URL         string  `json:"url"`
	Title       string  `json:"title,omitempty"`
	Snippet     string  `json:"snippet,omitempty"`
	SiteName    string  `json:"site_name,omitempty"`
	SiteIcon    string  `json:"site_icon,omitempty"`
	PublishedAt float64 `json:"published_at,omitempty"`
}

type citationLinkCollector struct {
	ordered     []string
	explicitRaw map[int]string
	hasZeroIdx  bool
	sources     map[int]CitationSource
}

func newCitationLinkCollector() *citationLinkCollector {
	return &citationLinkCollector{
		explicitRaw: map[int]string{},
		sources:     map[int]CitationSource{},
	}
}

func (c *citationLinkCollector) ingestChunk(chunk map[string]any) {
	if c == nil || len(chunk) == 0 {
		return
	}
	c.walkValue(chunk)
}

func (c *citationLinkCollector) build() map[int]string {
	out := make(map[int]string, len(c.explicitRaw)+len(c.ordered))
	for idx, u := range c.buildNormalizedExplicit() {
		out[idx] = u
	}
	for i, u := range c.ordered {
		idx := i + 1
		if _, exists := out[idx]; !exists {
			out[idx] = u
		}
	}
	return out
}

func (c *citationLinkCollector) buildSources() []CitationSource {
	// Merge explicit sources with ordered fallbacks
	merged := make(map[int]CitationSource, len(c.sources)+len(c.ordered))

	// First, add explicit sources
	for idx, src := range c.sources {
		if idx <= 0 || strings.TrimSpace(src.URL) == "" {
			continue
		}
		merged[idx] = src
	}

	// Handle zero-based index shift if needed
	if c.hasZeroIdx {
		for rawIdx, src := range c.sources {
			if rawIdx < 0 || strings.TrimSpace(src.URL) == "" {
				continue
			}
			normalized := rawIdx + 1
			existing, exists := merged[normalized]
			if !exists {
				merged[normalized] = src
				continue
			}
			if c.preferURLForIndex(normalized, existing.URL, src.URL) == src.URL {
				merged[normalized] = src
			}
		}
	}

	// Add ordered fallbacks for indices not covered by explicit sources
	for i, url := range c.ordered {
		idx := i + 1
		if _, exists := merged[idx]; !exists && isWebURL(url) {
			merged[idx] = CitationSource{Index: idx, URL: url}
		}
	}

	// Convert to sorted slice
	result := make([]CitationSource, 0, len(merged))
	for _, src := range merged {
		result = append(result, src)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Index < result[j].Index
	})

	return result
}

func (c *citationLinkCollector) buildNormalizedExplicit() map[int]string {
	out := make(map[int]string, len(c.explicitRaw))

	// Default behavior keeps positive indices as-is (one-based payloads).
	for idx, u := range c.explicitRaw {
		if idx <= 0 || strings.TrimSpace(u) == "" {
			continue
		}
		out[idx] = u
	}

	if !c.hasZeroIdx {
		return out
	}

	// If zero index appears, upstream may be using zero-based indices.
	// Add shifted candidates and resolve conflicts using ordered appearance,
	// which matches visible citation marker order in response text.
	for rawIdx, u := range c.explicitRaw {
		if rawIdx < 0 || strings.TrimSpace(u) == "" {
			continue
		}
		normalized := rawIdx + 1
		existing, exists := out[normalized]
		if !exists {
			out[normalized] = u
			continue
		}
		if c.preferURLForIndex(normalized, existing, u) == u {
			out[normalized] = u
		}
	}

	return out
}

func (c *citationLinkCollector) preferURLForIndex(idx int, current, candidate string) string {
	if idx <= 0 || idx > len(c.ordered) {
		return current
	}
	expected := c.ordered[idx-1]
	switch {
	case strings.TrimSpace(expected) == "":
		return current
	case candidate == expected && current != expected:
		return candidate
	default:
		return current
	}
}

func (c *citationLinkCollector) walkValue(v any) {
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			c.walkValue(item)
		}
	case map[string]any:
		c.captureURLAndIndex(x)
		for _, vv := range x {
			c.walkValue(vv)
		}
	}
}

func (c *citationLinkCollector) captureURLAndIndex(m map[string]any) {
	url := strings.TrimSpace(asString(m["url"]))
	if !isWebURL(url) {
		return
	}
	c.addOrdered(url)

	idx, hasIdx := citationIndexFromAny(m["cite_index"])
	if !hasIdx {
		return
	}
	if idx < 0 {
		return
	}
	if idx == 0 {
		c.hasZeroIdx = true
	}
	if existing, ok := c.explicitRaw[idx]; ok && strings.TrimSpace(existing) != "" {
		return
	}
	c.explicitRaw[idx] = url

	// Capture full source metadata
	src := CitationSource{
		Index:       idx,
		URL:         url,
		Title:       asString(m["title"]),
		Snippet:     asString(m["snippet"]),
		SiteName:    asString(m["site_name"]),
		SiteIcon:    asString(m["site_icon"]),
		PublishedAt: 0,
	}
	if pubAt, ok := m["published_at"].(float64); ok {
		src.PublishedAt = pubAt
	}

	// Only store if we don't already have a non-empty source for this index
	if existing, ok := c.sources[idx]; !ok || strings.TrimSpace(existing.URL) == "" {
		c.sources[idx] = src
	}
}

func (c *citationLinkCollector) addOrdered(url string) {
	c.ordered = append(c.ordered, url)
}

func citationIndexFromAny(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case float32:
		return int(x), true
	case float64:
		return int(x), true
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		n, err := strconv.Atoi(s)
		if err != nil {
			return 0, false
		}
		return n, true
	default:
		return 0, false
	}
}

func isWebURL(v string) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	return strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://")
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
