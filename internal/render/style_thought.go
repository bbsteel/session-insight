package render

import (
	"fmt"

	"github.com/bbsteel/session-insight/internal/model"
)

// grokThought renders Grok-style compact thought folds: a single "◆ Thought
// for Xs" header line, foldable body chunks, and a muted vertical sidebar.
type grokThought struct{}

func (grokThought) Start(p *Profile, tb *trackingBuilder, evt model.RenderEvent, prefix, ts string) *ThoughtFold {
	tb.WriteString(prefix)
	if ts != "" {
		tb.WriteString(fgWrap(ts+" ", p.Palette.Muted))
	}
	tb.WriteString(fgWrap("◆", p.Palette.Muted))
	tb.WriteString(styled(" "+sanitizeControlChars(evt.Text), p.Palette.Fg, ColNone, true, false))
	tb.WriteString("\n")

	hdrDisp := tb.CurrentLine() - 1
	hdrLog := tb.CurrentLogicalLine() - 1
	return &ThoughtFold{
		TurnIndex:     evt.TurnIndex,
		Key:           fmt.Sprintf("tfold:%d:%d", evt.TurnIndex, hdrDisp),
		HeaderDisplay: hdrDisp,
		HeaderLogical: hdrLog,
		BodyDisplay:   tb.CurrentLine(),
		BodyLogical:   tb.CurrentLogicalLine(),
	}
}

func (grokThought) Chunk(p *Profile, tb *trackingBuilder, evt model.RenderEvent, prefix string) {
	tb.WriteString(prefix)
	tb.WriteString(fgWrap(p.Box.V, p.Palette.Muted))
	tb.WriteString(" ")
	tb.WriteString(italicWrap(fgWrap(sanitizeControlChars(evt.Text), p.Palette.Muted)))
	tb.WriteString("\n")
}

func (grokThought) End(p *Profile, tb *trackingBuilder, fold *ThoughtFold) *RenderPosition {
	if tb.CurrentLine() <= fold.BodyDisplay {
		return nil
	}
	endIncl := tb.CurrentLine() - 1
	return &RenderPosition{
		PositionKey: fold.Key,
		Kind:        "fold",
		TurnIndex:   fold.TurnIndex,
		LineStart:   fold.HeaderDisplay,
		LineEnd:     &endIncl,
		Label:       "thought",
		Payload: map[string]any{
			"level":          "thought",
			"group_key":      "",
			"display_start":  float64(fold.BodyDisplay),
			"display_end":    float64(tb.CurrentLine()),
			"logical_start":  float64(fold.BodyLogical),
			"logical_end":    float64(tb.CurrentLogicalLine()),
			"header_logical": float64(fold.HeaderLogical),
		},
	}
}
