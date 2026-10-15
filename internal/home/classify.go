package home

// UnfinishedReasons returns every positive reason the latest ending supports.
// A live session is not unfinished. A normal close with no open todo and no
// missing shutdown returns no reasons: the session is unknown, not finished.
func UnfinishedReasons(signals Signals) []string {
	if signals.Live {
		return nil
	}
	reasons := make([]string, 0, 3)
	switch {
	case signals.Quota:
		reasons = append(reasons, ReasonQuotaStop)
	case signals.UserInterrupt:
		reasons = append(reasons, ReasonTemporaryExit)
	case signals.TurnOpen:
		reasons = append(reasons, ReasonProcessExit)
	}
	if signals.OpenTodos {
		reasons = append(reasons, ReasonOpenTodo)
	}
	if signals.MissingShutdown {
		reasons = append(reasons, ReasonMissingShutdown)
	}
	return reasons
}
