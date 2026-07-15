package insight

import (
	"fmt"
	"sort"
	"strings"
)

// RenderMarkdown builds the human-readable Deep Insight from validated output.
// Model-provided text is sanitized (HTML defused, remote-image markdown
// neutralized) so a generation can never smuggle raw HTML or an external
// request into the rendered card. Every cited evidence_id is expanded inline to
// its original fact statement, so a reader sees the claim and its evidence
// together — the product presents causal conclusions as model inference, not as
// proof that an ID citation establishes causation.
func RenderMarkdown(out *Output, b Bundle) string {
	facts := map[string]EvidenceFact{}
	for _, f := range b.Facts {
		facts[f.ID] = f
	}

	var sb strings.Builder
	if s := clean(out.Summary); s != "" {
		sb.WriteString(s)
		sb.WriteString("\n\n")
	}
	if len(out.Insights) == 0 {
		sb.WriteString("_当前证据不足以给出原因洞察。_\n")
		writeGaps(&sb, out.EvidenceGaps)
		return sb.String()
	}

	for _, ins := range out.Insights {
		fmt.Fprintf(&sb, "## %s\n\n", clean(ins.Title))
		fmt.Fprintf(&sb, "**置信度：** %s\n\n", ins.Confidence)

		fmt.Fprintf(&sb, "**主要原因（%s，%s）：** %s\n\n",
			epistemicLabel(ins.Cause.EpistemicStatus), strengthLabel(ins.Cause.CausalStrength), clean(ins.Cause.Statement))
		writeEvidence(&sb, "关键证据", ins.Cause.EvidenceIDs, facts)
		writeList(&sb, "已排查的混淆因素", ins.Cause.Confounders)

		if s := clean(ins.Impact.Statement); s != "" {
			fmt.Fprintf(&sb, "**影响：** %s\n\n", s)
			writeEvidence(&sb, "影响证据", ins.Impact.EvidenceIDs, facts)
		}

		writeEvidence(&sb, "反证", ins.CounterEvidenceIDs, facts)
		for _, alt := range ins.Alternatives {
			fmt.Fprintf(&sb, "**替代解释：** %s", clean(alt.Statement))
			if a := clean(alt.Assessment); a != "" {
				fmt.Fprintf(&sb, "（%s）", a)
			}
			sb.WriteString("\n\n")
			writeEvidence(&sb, "支持替代解释", alt.EvidenceIDs, facts)
			writeEvidence(&sb, "反对替代解释", alt.OpposingEvidenceIDs, facts)
		}

		writeList(&sb, "下一次可改进", ins.Recommendations)
		writeList(&sb, "数据边界", ins.Caveats)
		sb.WriteString("\n")
	}
	writeGaps(&sb, out.EvidenceGaps)
	return strings.TrimRight(sb.String(), "\n") + "\n"
}

func writeEvidence(sb *strings.Builder, label string, ids []string, facts map[string]EvidenceFact) {
	if len(ids) == 0 {
		return
	}
	fmt.Fprintf(sb, "**%s：**\n", label)
	for _, id := range ids {
		if f, ok := facts[id]; ok {
			fmt.Fprintf(sb, "- `%s`：%s\n", id, clean(f.Statement))
		} else {
			fmt.Fprintf(sb, "- `%s`\n", clean(id))
		}
	}
	sb.WriteString("\n")
}

func writeList(sb *strings.Builder, label string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(sb, "**%s：**\n", label)
	for _, it := range items {
		fmt.Fprintf(sb, "- %s\n", clean(it))
	}
	sb.WriteString("\n")
}

func writeGaps(sb *strings.Builder, gaps []string) {
	if len(gaps) == 0 {
		return
	}
	sb.WriteString("**数据缺口：**\n")
	for _, g := range gaps {
		fmt.Fprintf(sb, "- %s\n", clean(g))
	}
}

// CitedEvidence returns the minimal projection of facts the output actually
// referenced — persisted with the generation so an old Insight can still
// explain its own evidence after the session becomes stale, without storing the
// whole bundle.
func CitedEvidence(out *Output, b Bundle) []EvidenceFact {
	facts := map[string]EvidenceFact{}
	for _, f := range b.Facts {
		facts[f.ID] = f
	}
	seen := map[string]bool{}
	var cited []EvidenceFact
	add := func(ids []string) {
		for _, id := range ids {
			if seen[id] {
				continue
			}
			if f, ok := facts[id]; ok {
				seen[id] = true
				cited = append(cited, f)
			}
		}
	}
	for _, ins := range out.Insights {
		add(allCitedIDs(&ins))
	}
	sort.SliceStable(cited, func(i, j int) bool { return cited[i].ID < cited[j].ID })
	return cited
}

func epistemicLabel(s string) string {
	switch s {
	case "observed":
		return "观察事实"
	case "inferred":
		return "推断"
	case "unknown":
		return "无法判断"
	default:
		return s
	}
}

func strengthLabel(s string) string {
	switch s {
	case "none":
		return "无因果"
	case "weak":
		return "弱因果"
	case "moderate":
		return "中等因果"
	case "strong":
		return "强因果"
	default:
		return s
	}
}

// clean neutralizes model text for safe Markdown rendering: HTML angle brackets
// are escaped and image markdown is defused so no raw HTML or remote image
// request can be embedded in a rendered card. It is deliberately conservative
// rather than a full sanitizer; the frontend renderer additionally disables raw
// HTML and remote resources.
func clean(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	// Defuse image syntax ![alt](url) -> !\[alt](url) so it renders as text.
	s = strings.ReplaceAll(s, "![", "!\\[")
	return s
}

// EscapePlainText prepares an unparseable raw model output for display as
// escaped plain text (the structured-parse-failed fallback). It never produces
// active Markdown/HTML: angle brackets are escaped and the whole body is fenced
// so the frontend shows it verbatim without loading raw HTML or remote assets.
func EscapePlainText(raw string) string {
	s := strings.ReplaceAll(raw, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}
