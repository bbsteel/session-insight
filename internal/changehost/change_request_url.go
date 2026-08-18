package changehost

import (
	"strconv"
	"strings"
)

// changeRequestPathIdentity reports whether path looks like a hosted pull,
// merge, or review request. It does not guess a provider; callers still treat
// unrecognized hosts as generic, offline identities.
func changeRequestPathIdentity(path string) (slug, number string, ok bool) {
	segments := make([]string, 0, 8)
	for _, segment := range strings.Split(path, "/") {
		if segment == "" {
			continue
		}
		segments = append(segments, segment)
	}
	for index, segment := range segments {
		switch strings.ToLower(segment) {
		case "pull", "pulls", "merge_requests", "pull-requests", "pullrequest", "pullrequests", "reviews":
			if index+1 >= len(segments) || !positiveDisplayNumberOK(segments[index+1]) {
				continue
			}
			end := index
			if end > 0 && segments[end-1] == "-" {
				end--
			}
			slug = strings.Join(segments[:end], "/")
			if slug == "" {
				return "", "", false
			}
			return slug, segments[index+1], true
		case "+":
			if index == 0 || !strings.EqualFold(segments[0], "c") ||
				index+1 >= len(segments) || !positiveDisplayNumberOK(segments[index+1]) {
				continue
			}
			slug = strings.Join(segments[1:index], "/")
			if slug == "" {
				return "", "", false
			}
			return slug, segments[index+1], true
		}
	}
	return "", "", false
}

func positiveDisplayNumberOK(value string) bool {
	number, err := strconv.Atoi(value)
	return err == nil && number > 0 && value == strconv.Itoa(number)
}
