package shared

import "strings"

// TruncateRunes truncates a string to n runes, appending "..." if truncated.
func TruncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

// BuildPreviewText truncates each message to 200 runes, joins them with " | ",
// and caps the result at ~1500 bytes.
func BuildPreviewText(messages []string) string {
	const maxPerMsg = 200
	const maxTotal = 1500
	var parts []string
	for _, m := range messages {
		parts = append(parts, TruncateRunes(m, maxPerMsg))
	}
	joined := strings.Join(parts, " | ")
	if len(joined) > maxTotal {
		return joined[:maxTotal] + "..."
	}
	return joined
}

// EstimateLineCount estimates total line count from a head scan, using
// average line length with a 1.1× fudge factor for longer later lines.
func EstimateLineCount(headLines int, headBytes int64, fileSize int64) int {
	if headLines == 0 || headBytes == 0 {
		return 0
	}
	avgLineLen := float64(headBytes) / float64(headLines)
	return int(float64(fileSize) / avgLineLen * 1.1)
}
