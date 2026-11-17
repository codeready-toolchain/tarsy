package services

import (
	"regexp"
	"strings"
)

const maxPostInvestigationLinks = 5

// http(s) URLs in a memo, including markdown links. Stops at whitespace and
// the characters that wrap a URL in markdown or prose.
var postInvestigationURLPattern = regexp.MustCompile(`https?://[^\s<>()\[\]"']+`)

// postInvestigationLinks extracts artifact URLs from the concatenated
// post-investigation memos selected for a list row. Nil when there are none.
func postInvestigationLinks(memos *string) []string {
	if memos == nil || *memos == "" {
		return nil
	}
	return extractPostInvestigationLinks(*memos)
}

// extractPostInvestigationLinks returns http(s) URLs in first-seen order.
// Trailing punctuation is stripped, duplicates are dropped, and the result
// is capped. Nil when memos contain no URL.
func extractPostInvestigationLinks(memos string) []string {
	matches := postInvestigationURLPattern.FindAllString(memos, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	var links []string
	for _, raw := range matches {
		link := strings.TrimRight(raw, ".,;:!?")
		if link == "" {
			continue
		}
		if _, ok := seen[link]; ok {
			continue
		}
		seen[link] = struct{}{}
		links = append(links, link)
		if len(links) == maxPostInvestigationLinks {
			break
		}
	}
	if len(links) == 0 {
		return nil
	}
	return links
}
