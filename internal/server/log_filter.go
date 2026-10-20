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

// Filter replaces the text each pattern selects in the path. Every pattern is
// matched against the original path, so one pattern's replacement can't stop
// another from matching.
func (f *LogPathFilter) Filter(path string) string {
	if f == nil {
		return path
	}

	spans := [][2]int{}
	for _, re := range f.patterns {
		spans = append(spans, matchedSpans(re, path)...)
	}
	return replaceSpans(path, spans)
}

// LoggedPath returns the request path as it should appear in logs. For a
// request routed to a service with a log filter, that's the path the client
// requested, filtered, even after the path has been rewritten for the target.
func LoggedPath(r *http.Request) string {
	return LoggingRequestContext(r).loggedPath(r)
}

// Private

func matchedSpans(re *regexp.Regexp, s string) [][2]int {
	spans := [][2]int{}
	for _, match := range re.FindAllStringSubmatchIndex(s, -1) {
		// Without capture groups, the whole match is the only span
		first := 2
		if re.NumSubexp() == 0 {
			first = 0
		}

		for i := first; i < len(match); i += 2 {
			if match[i] >= 0 && match[i] < match[i+1] {
				spans = append(spans, [2]int{match[i], match[i+1]})
			}
		}
	}

	return spans
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
