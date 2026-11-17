package home

import "time"

// Reason codes are stable API values. The frontend translates them.
const (
	ReasonTemporaryExit   = "temporary_exit"
	ReasonQuotaStop       = "quota_stop"
	ReasonProcessExit     = "process_exit"
	ReasonOpenTodo        = "open_todo"
	ReasonMissingShutdown = "missing_shutdown"
)

// Focus kinds narrow the whole home, including live and unfinished sessions.
const (
	FocusHealth = "health"
	FocusTool   = "tool"
	FocusSkill  = "skill"
)

// Health values are the four evidence counts. There is no combined score.
const (
	HealthToolFailure       = "tool_failure"
	HealthDurationSpike     = "duration_spike"
	HealthContinuationNudge = "continuation_nudge"
	HealthMissingShutdown   = "missing_shutdown"
)

// Signals is the latest-ending evidence for one root session.
type Signals struct {
	Live            bool
	Quota           bool
	UserInterrupt   bool
	TurnOpen        bool
	OpenTodos       bool
	MissingShutdown bool
}

// TokenEvent is one timestamped token observation. Reasoning is not a bucket:
// it is already inside completion.
type TokenEvent struct {
	At                 time.Time
	Prompt             int64
	CacheRead          int64
	CacheWrite         int64
	Completion         int64
	CacheReadPresence  string
	CacheWritePresence string
	InputPresence      string
	OutputPresence     string
}

// TimedName is one tool call or skill occurrence.
type TimedName struct {
	Name string
	At   time.Time
}

// TurnHealth is evidence attached to one turn.
type TurnHealth struct {
	At                time.Time
	ToolFailure       bool
	DurationSpike     bool
	ContinuationNudge bool
}

// CostItem is one session bill. Different units are never added together.
type CostItem struct {
	Unit      string
	Amount    float64
	Precision string
	// ActivityOutside is true when this session also has activity outside the
	// window that included the bill. The amount then cannot stay exact.
	ActivityOutside bool
}

// CodeChange is one file change. Zero RecordedAt means the evidence has no time.
type CodeChange struct {
	Path       string
	Additions  int
	Deletions  int
	HasLines   bool
	RecordedAt time.Time
}

// SessionFact is the home's view of one root session after child usage that
// can be attributed has been absorbed. Child sessions do not become rows.
type SessionFact struct {
	ID            string
	AgentType     string
	Project       string
	Name          string
	UpdatedAt     time.Time
	Live          bool
	Bookmarked    bool
	DetailMissing bool

	UserUtterances      []time.Time
	AssistantUtterances []time.Time
	UntimedUtterances   int
	Turns               []time.Time

	Tokens            []TokenEvent
	UntimedTokens     bool
	MissingCacheRead  bool
	MissingCacheWrite bool

	Tools           []TimedName
	Skills          []TimedName
	UntimedSkills   int
	TurnHealth      []TurnHealth
	MissingShutdown bool

	Signals Signals
	Costs   []CostItem
	// HasBill is false when this session contributed no billing amount.
	HasBill bool

	Code        []CodeChange
	UntimedCode int
}

// Clone copies slice fields so a cached fact can be absorbed into without
// aliasing the cache.
func (fact SessionFact) Clone() SessionFact {
	fact.UserUtterances = append([]time.Time(nil), fact.UserUtterances...)
	fact.AssistantUtterances = append([]time.Time(nil), fact.AssistantUtterances...)
	fact.Turns = append([]time.Time(nil), fact.Turns...)
	fact.Tokens = append([]TokenEvent(nil), fact.Tokens...)
	fact.Tools = append([]TimedName(nil), fact.Tools...)
	fact.Skills = append([]TimedName(nil), fact.Skills...)
	fact.TurnHealth = append([]TurnHealth(nil), fact.TurnHealth...)
	fact.Costs = append([]CostItem(nil), fact.Costs...)
	fact.Code = append([]CodeChange(nil), fact.Code...)
	return fact
}

// Query is the home request. Day, when set, replaces the 7- or 30-day span
// for windowed modules only.
type Query struct {
	WindowDays int
	Day        string
	Project    string
	Agent      string
	FocusKind  string
	FocusValue string
	Now        time.Time
	Location   *time.Location
}
