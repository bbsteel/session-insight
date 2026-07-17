package render

// standardBullet is the default per-tool bullet used by chrys.
type standardBullet struct {
	char string
}

func (b standardBullet) Char() string {
	if b.char == "" {
		return "•"
	}
	return b.char
}

func (b standardBullet) ColorForTool(p *Profile, toolName string, failed bool) Color {
	return p.Palette.Fg
}

func (b standardBullet) WriteFoldHeader(p *Profile, tb *trackingBuilder, toolName string, failed bool,
	purpose string, input map[string]any) int {
	bullet := "▼ " + b.Char() + " "
	nameRun := styled(bullet+toolName, b.ColorForTool(p, toolName, failed), ColNone, true, false)
	tb.WriteString(nameRun)
	if s := toolSummary(purpose, input); s != "" {
		tb.WriteString(styled("  "+s, b.ColorForTool(p, toolName, failed), ColNone, true, false))
	}
	return utf16Len(nameRun)
}

func (b standardBullet) WriteInlineHeader(p *Profile, tb *trackingBuilder, toolName string, failed bool,
	purpose string, input map[string]any) {
	tb.WriteString(styled(b.Char()+" "+toolName, b.ColorForTool(p, toolName, failed), ColNone, true, false))
}

func (b standardBullet) WriteEditHeader(p *Profile, tb *trackingBuilder, prefix, ts, toolName string) {
	// Chrys does not render a bullet above edit diffs.
}

// grokBullet renders Grok Build's native "◆" tool bullets with per-tool colors.
type grokBullet struct{}

func (grokBullet) Char() string { return "◆" }

func (grokBullet) ColorForTool(p *Profile, toolName string, failed bool) Color {
	switch toolName {
	case "Run":
		if failed {
			return p.Palette.ErrorBright
		}
		return p.Palette.Success
	case "Skill":
		return p.Palette.Skill
	}
	return p.Palette.Success
}

func (grokBullet) WriteFoldHeader(p *Profile, tb *trackingBuilder, toolName string, failed bool,
	purpose string, input map[string]any) int {
	c := grokBullet{}.ColorForTool(p, toolName, failed)
	diamond := fgWrap(grokBullet{}.Char(), c)
	tb.WriteString(diamond)
	nameRun := styled(" "+toolName, p.Palette.Fg, ColNone, true, false)
	tb.WriteString(nameRun)
	offset := utf16Len(diamond) + utf16Len(nameRun)

	if s := toolSummary(purpose, input); s != "" {
		summaryColor := p.Palette.Fg
		if toolName == "Skill" {
			summaryColor = p.Palette.Skill
		}
		tb.WriteString(styled(" "+s, summaryColor, ColNone, true, false))
	}
	return offset
}

func (grokBullet) WriteInlineHeader(p *Profile, tb *trackingBuilder, toolName string, failed bool,
	purpose string, input map[string]any) {
	c := grokBullet{}.ColorForTool(p, toolName, failed)
	tb.WriteString(fgWrap(grokBullet{}.Char(), c))
	tb.WriteString(styled(" "+toolName, p.Palette.Fg, ColNone, true, false))
	if toolName == "Skill" {
		if s := toolSummary(purpose, input); s != "" {
			tb.WriteString(styled(" "+s, p.Palette.Skill, ColNone, true, false))
		}
	}
}

func (grokBullet) WriteEditHeader(p *Profile, tb *trackingBuilder, prefix, ts, toolName string) {
	tb.WriteString(prefix)
	if ts != "" {
		tb.WriteString(fgWrap(ts+" ", p.Palette.Muted))
	}
	tb.WriteString(styled("◆ SearchReplace", p.Palette.Fg, ColNone, true, false))
	tb.WriteString("\n")
}

// defaultBullet is the fallback used when a profile enables ToolBullet but does
// not provide a specific bullet strategy.
type defaultBullet struct{}

func (defaultBullet) Char() string { return "•" }

func (defaultBullet) ColorForTool(p *Profile, toolName string, failed bool) Color {
	return p.Palette.Fg
}

func (defaultBullet) WriteFoldHeader(p *Profile, tb *trackingBuilder, toolName string, failed bool,
	purpose string, input map[string]any) int {
	return standardBullet{char: "•"}.WriteFoldHeader(p, tb, toolName, failed, purpose, input)
}

func (defaultBullet) WriteInlineHeader(p *Profile, tb *trackingBuilder, toolName string, failed bool,
	purpose string, input map[string]any) {
	standardBullet{char: "•"}.WriteInlineHeader(p, tb, toolName, failed, purpose, input)
}

func (defaultBullet) WriteEditHeader(p *Profile, tb *trackingBuilder, prefix, ts, toolName string) {}
