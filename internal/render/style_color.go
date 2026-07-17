package render

// grokColorRule overrides the tool-input-box border color for Grok-style Run
// tools: green when successful, bright red when failed. All other tools fall
// back to the base category color computed by categoryColor.
type grokColorRule struct{}

func (grokColorRule) ColorFor(p *Profile, toolName string, failed bool) (Color, bool) {
	if toolName == "Run" {
		if failed {
			return p.Palette.ErrorBright, true
		}
		return p.Palette.Success, true
	}
	return ColNone, false
}
