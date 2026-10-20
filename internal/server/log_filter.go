package server

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
)

const FilteredLogValue = "[FILTERED]"

// LogPathFilter replaces the sensitive parts of a request path before it is
// logged, such as tokens that a URL carries in a path segment.
//
// Each pattern is a regular expression matched against the decoded request
// path. Where a pattern has capture groups, the text each group matches is
// replaced; otherwise the whole match is replaced.
type LogPathFilter struct {
	patterns []*regexp.Regexp
}

func NewLogPathFilter(patterns []string) (*LogPathFilter, error) {
	if len(patterns) == 0 {
		return nil, nil
	}

	filter := &LogPathFilter{}
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid log filter path %q: %w", pattern, err)
		}
		filter.patterns = append(filter.patterns, re)
	}

	return filter, nil
}

func (f *LogPathFilter) Filter(path string) string {
	if f == nil {
		return path
	}

	for _, re := range f.patterns {
		path = filterMatches(re, path)
	}
	return path
}

// LoggedPath returns the request path as it should appear in logs, with the
// sensitive parts that the service's log filter matches replaced.
func LoggedPath(r *http.Request) string {
	return LoggingRequestContext(r).PathFilter.Filter(r.URL.Path)
}

// Private

func filterMatches(re *regexp.Regexp, s string) string {
	matches := re.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return s
	}

	spans := [][2]int{}
	for _, match := range matches {
		if re.NumSubexp() == 0 {
			spans = append(spans, [2]int{match[0], match[1]})
			continue
		}

		for i := 2; i < len(match); i += 2 {
			if match[i] >= 0 && match[i] < match[i+1] {
				spans = append(spans, [2]int{match[i], match[i+1]})
			}
		}
	}

	return replaceSpans(s, spans)
}

// replaceSpans replaces each span with the filtered marker. A span that
// overlaps one already replaced, such as a nested capture group, is merged
// into it.
func replaceSpans(s string, spans [][2]int) string {
	slices.SortFunc(spans, func(a, b [2]int) int { return a[0] - b[0] })

	result := []byte{}
	last := 0
	for _, span := range spans {
		start, end := span[0], span[1]
		if start < last {
			if end > last {
				last = end
			}
			continue
		}

		result = append(result, s[last:start]...)
		result = append(result, FilteredLogValue...)
		last = end
	}
	result = append(result, s[last:]...)

	return string(result)
}
