package changehost

import (
	"fmt"
	"strconv"
	"strings"
)

type githubDiffSection struct {
	OldPath string
	NewPath string
	OldMode string
	NewMode string
	Patch   []byte
}

func parseGitHubDiff(raw []byte) ([]githubDiffSection, error) {
	if len(raw) == 0 {
		return []githubDiffSection{}, nil
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	lines := strings.SplitAfter(text, "\n")
	sections := make([]githubDiffSection, 0)
	start := -1
	flush := func(end int) error {
		if start < 0 {
			return nil
		}
		section, err := parseGitHubDiffSection(lines[start:end])
		if err != nil {
			return err
		}
		sections = append(sections, section)
		return nil
	}
	for index, line := range lines {
		if strings.HasPrefix(line, "diff --git ") {
			if err := flush(index); err != nil {
				return nil, err
			}
			start = index
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("GitHub diff has no file sections")
	}
	if err := flush(len(lines)); err != nil {
		return nil, err
	}
	return sections, nil
}

func parseGitHubDiffSection(lines []string) (githubDiffSection, error) {
	section := githubDiffSection{Patch: []byte(strings.Join(lines, ""))}
	var indexMode string
	for _, rawLine := range lines[1:] {
		line := strings.TrimSuffix(rawLine, "\n")
		switch {
		case strings.HasPrefix(line, "old mode "):
			section.OldMode = strings.TrimPrefix(line, "old mode ")
		case strings.HasPrefix(line, "new mode "):
			section.NewMode = strings.TrimPrefix(line, "new mode ")
		case strings.HasPrefix(line, "new file mode "):
			section.OldMode = "000000"
			section.NewMode = strings.TrimPrefix(line, "new file mode ")
		case strings.HasPrefix(line, "deleted file mode "):
			section.OldMode = strings.TrimPrefix(line, "deleted file mode ")
			section.NewMode = "000000"
		case strings.HasPrefix(line, "index "):
			fields := strings.Fields(line)
			if len(fields) == 3 {
				indexMode = fields[2]
			}
		case strings.HasPrefix(line, "rename from "):
			section.OldPath = decodeGitHubDiffPath(strings.TrimPrefix(line, "rename from "), "")
		case strings.HasPrefix(line, "rename to "):
			section.NewPath = decodeGitHubDiffPath(strings.TrimPrefix(line, "rename to "), "")
		case strings.HasPrefix(line, "copy from "):
			section.OldPath = decodeGitHubDiffPath(strings.TrimPrefix(line, "copy from "), "")
		case strings.HasPrefix(line, "copy to "):
			section.NewPath = decodeGitHubDiffPath(strings.TrimPrefix(line, "copy to "), "")
		case strings.HasPrefix(line, "--- ") && section.OldPath == "":
			section.OldPath = decodeGitHubDiffPath(strings.TrimPrefix(line, "--- "), "a/")
		case strings.HasPrefix(line, "+++ ") && section.NewPath == "":
			section.NewPath = decodeGitHubDiffPath(strings.TrimPrefix(line, "+++ "), "b/")
		}
	}
	if section.OldMode == "" {
		section.OldMode = indexMode
	}
	if section.NewMode == "" {
		section.NewMode = indexMode
	}
	if section.OldPath == "" && section.NewPath == "" {
		return githubDiffSection{}, fmt.Errorf("GitHub diff section has no decodable paths")
	}
	if section.OldPath != "" && !safeProviderPath(section.OldPath) || section.NewPath != "" && !safeProviderPath(section.NewPath) {
		return githubDiffSection{}, fmt.Errorf("GitHub diff section has unsafe path")
	}
	return section, nil
}

func decodeGitHubDiffPath(raw, prefix string) string {
	raw = strings.TrimSpace(raw)
	if raw == "/dev/null" {
		return ""
	}
	if strings.HasPrefix(raw, `"`) {
		decoded, err := strconv.Unquote(raw)
		if err != nil {
			return ""
		}
		raw = decoded
	}
	return strings.TrimPrefix(raw, prefix)
}

func githubDiffSectionsByPath(sections []githubDiffSection) map[string]githubDiffSection {
	result := make(map[string]githubDiffSection, len(sections))
	for _, section := range sections {
		result[section.OldPath+"\x00"+section.NewPath] = section
	}
	return result
}
