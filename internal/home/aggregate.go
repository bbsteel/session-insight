package home

import (
	"sort"
	"time"
)

// Report is the session-home payload. Windowed modules use the active span.
// Live and unfinished sessions ignore that span and still follow project,
// agent, and focus.
type Report struct {
	GeneratedAt time.Time        `json:"generated_at"`
	Timezone    string           `json:"timezone"`
	WindowDays  int              `json:"window_days"`
	Day         string           `json:"day,omitempty"`
	Project     string           `json:"project,omitempty"`
	Agent       string           `json:"agent,omitempty"`
	FocusKind   string           `json:"focus_kind,omitempty"`
	FocusValue  string           `json:"focus_value,omitempty"`
	Summary     Summary          `json:"summary"`
	Days        []DayActivity    `json:"days"`
	Live        []SessionCard    `json:"live"`
	Unfinished  []UnfinishedCard `json:"unfinished"`
	Recent      []SessionCard    `json:"recent"`
	Starred     []SessionCard    `json:"starred"`
	Projects    []CountRow       `json:"projects"`
	Agents      []CountRow       `json:"agents"`
	Health      HealthCounts     `json:"health"`
	Tools       []CountRow       `json:"tools"`
	Skills      []CountRow       `json:"skills"`
	Coverage    Coverage         `json:"coverage"`
}

type Summary struct {
	Sessions  int         `json:"sessions"`
	Projects  int         `json:"projects"`
	Agents    int         `json:"agents"`
	Messages  int         `json:"messages"`
	Turns     int         `json:"turns"`
	Tokens    int64       `json:"tokens"`
	Costs     []CostTotal `json:"costs"`
	CodeFiles int         `json:"code_files"`
	Additions int         `json:"additions"`
	Deletions int         `json:"deletions"`
}

type CostTotal struct {
	Unit      string  `json:"unit"`
	Amount    float64 `json:"amount"`
	Precision string  `json:"precision"`
}

type DayActivity struct {
	Date     string `json:"date"`
	Messages int    `json:"messages"`
	Turns    int    `json:"turns"`
	Tokens   int64  `json:"tokens"`
}

type SessionCard struct {
	ID         string    `json:"id"`
	AgentType  string    `json:"agent_type"`
	Project    string    `json:"project,omitempty"`
	Name       string    `json:"name,omitempty"`
	UpdatedAt  time.Time `json:"updated_at"`
	Bookmarked bool      `json:"bookmarked,omitempty"`
}

type UnfinishedCard struct {
	SessionCard
	Reasons []string `json:"reasons"`
}

type CountRow struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type HealthCounts struct {
	ToolFailures       int `json:"tool_failures"`
	DurationSpikes     int `json:"duration_spikes"`
	ContinuationNudges int `json:"continuation_nudges"`
	MissingShutdowns   int `json:"missing_shutdowns"`
}

type Coverage struct {
	UntimedUtterances         int      `json:"untimed_utterances"`
	UntimedSkills             int      `json:"untimed_skills"`
	MissingCacheReadSessions  int      `json:"missing_cache_read_sessions"`
	MissingCacheWriteSessions int      `json:"missing_cache_write_sessions"`
	UntimedTokenSessions      int      `json:"untimed_token_sessions"`
	CostOmittedSessions       int      `json:"cost_omitted_sessions"`
	SpanningCostSessions      int      `json:"spanning_cost_sessions"`
	UntimedCodeChanges        int      `json:"untimed_code_changes"`
	CodeSessions              int      `json:"code_sessions"`
	UnattributedChildSessions int      `json:"unattributed_child_sessions"`
	SkillUncoveredAgents      []string `json:"skill_uncovered_agents"`
	DetailMissingSessions     int      `json:"detail_missing_sessions"`
}

// skillRecordingAgents are the adapters that attach skill names today.
var skillRecordingAgents = map[string]bool{
	"grok": true, "copilot": true, "chrys": true,
}

// Aggregate builds the home report. facts are root sessions only.
func Aggregate(facts []SessionFact, query Query, generatedAt time.Time) Report {
	loc := query.Location
	if loc == nil {
		loc = time.Local
	}
	start, end := span(query, loc)
	report := Report{
		GeneratedAt: generatedAt,
		Timezone:    loc.String(),
		WindowDays:  query.WindowDays,
		Day:         query.Day,
		Project:     query.Project,
		Agent:       query.Agent,
		FocusKind:   query.FocusKind,
		FocusValue:  query.FocusValue,
		Days:        emptyDays(start, end, loc),
		Live:        []SessionCard{},
		Unfinished:  []UnfinishedCard{},
		Recent:      []SessionCard{},
		Starred:     []SessionCard{},
		Projects:    []CountRow{},
		Agents:      []CountRow{},
		Tools:       []CountRow{},
		Skills:      []CountRow{},
	}
	dayIndex := map[string]int{}
	for i, day := range report.Days {
		dayIndex[day.Date] = i
	}
	projects := map[string]int{}
	agents := map[string]int{}
	tools := map[string]int{}
	skills := map[string]int{}
	costs := map[string]*CostTotal{}
	skillAgents := map[string]bool{}
	codeSessions := map[string]bool{}

	for _, fact := range facts {
		if !matchesIdentity(fact, query) {
			continue
		}
		if matchesFocus(fact, query, time.Time{}, time.Time{}, false) && fact.Live {
			report.Live = append(report.Live, card(fact))
		}
		if reasons := UnfinishedReasons(fact.Signals); len(reasons) > 0 && matchesFocus(fact, query, time.Time{}, time.Time{}, false) {
			report.Unfinished = append(report.Unfinished, UnfinishedCard{SessionCard: card(fact), Reasons: reasons})
		}
		if !inSpan(fact, start, end) || !matchesFocus(fact, query, start, end, true) {
			continue
		}
		report.Summary.Sessions++
		projects[fact.Project]++
		agents[fact.AgentType]++
		if !skillRecordingAgents[fact.AgentType] {
			skillAgents[fact.AgentType] = true
		}
		if fact.DetailMissing {
			report.Coverage.DetailMissingSessions++
		}
		report.Recent = append(report.Recent, card(fact))
		if fact.Bookmarked {
			report.Starred = append(report.Starred, card(fact))
		}
		report.Summary.Messages += countIn(fact.UserUtterances, start, end) + countIn(fact.AssistantUtterances, start, end)
		report.Summary.Turns += countIn(fact.Turns, start, end)
		report.Coverage.UntimedUtterances += fact.UntimedUtterances
		addDays(&report, dayIndex, loc, fact, start, end)
		addTokens(&report, fact, start, end)
		addNames(tools, fact.Tools, start, end)
		addNames(skills, fact.Skills, start, end)
		report.Coverage.UntimedSkills += fact.UntimedSkills
		addHealth(&report.Health, fact, start, end)
		addCosts(costs, &report.Coverage, fact, start, end)
		if added := addCode(&report.Summary, fact, start, end); added {
			codeSessions[fact.AgentType+"\x00"+fact.ID] = true
		}
		report.Coverage.UntimedCodeChanges += fact.UntimedCode
		if fact.MissingCacheRead {
			report.Coverage.MissingCacheReadSessions++
		}
		if fact.MissingCacheWrite {
			report.Coverage.MissingCacheWriteSessions++
		}
		if fact.UntimedTokens {
			report.Coverage.UntimedTokenSessions++
		}
		if !fact.HasBill {
			report.Coverage.CostOmittedSessions++
		}
	}

	report.Summary.Projects = len(projects)
	report.Summary.Agents = len(agents)
	report.Summary.Costs = costList(costs)
	report.Projects = countList(projects)
	report.Agents = countList(agents)
	report.Tools = countList(tools)
	report.Skills = countList(skills)
	report.Coverage.CodeSessions = len(codeSessions)
	report.Coverage.SkillUncoveredAgents = setList(skillAgents)
	sortCards(report.Live)
	sortCards(report.Recent)
	sortCards(report.Starred)
	sort.Slice(report.Unfinished, func(i, j int) bool {
		return newer(report.Unfinished[i].SessionCard, report.Unfinished[j].SessionCard)
	})
	return report
}

func span(query Query, loc *time.Location) (time.Time, time.Time) {
	now := query.Now.In(loc)
	if query.Day != "" {
		if day, err := time.ParseInLocation("2006-01-02", query.Day, loc); err == nil {
			return day, day.AddDate(0, 0, 1)
		}
	}
	days := query.WindowDays
	if days != 30 {
		days = 7
	}
	endDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	return endDay.AddDate(0, 0, -days), endDay
}

func emptyDays(start, end time.Time, loc *time.Location) []DayActivity {
	var days []DayActivity
	for cursor := start.In(loc); cursor.Before(end); cursor = cursor.AddDate(0, 0, 1) {
		days = append(days, DayActivity{Date: cursor.Format("2006-01-02")})
	}
	return days
}

func matchesIdentity(fact SessionFact, query Query) bool {
	if query.Project != "" && fact.Project != query.Project {
		return false
	}
	if query.Agent != "" && fact.AgentType != query.Agent {
		return false
	}
	return true
}

func matchesFocus(fact SessionFact, query Query, start, end time.Time, bounded bool) bool {
	if query.FocusKind == "" || query.FocusValue == "" {
		return true
	}
	switch query.FocusKind {
	case FocusHealth:
		return hasHealth(fact, query.FocusValue, start, end, bounded)
	case FocusTool:
		return hasName(fact.Tools, query.FocusValue, start, end, bounded)
	case FocusSkill:
		return hasName(fact.Skills, query.FocusValue, start, end, bounded)
	default:
		return true
	}
}

func hasHealth(fact SessionFact, value string, start, end time.Time, bounded bool) bool {
	if value == HealthMissingShutdown {
		return fact.MissingShutdown
	}
	for _, turn := range fact.TurnHealth {
		if bounded && !inside(turn.At, start, end) {
			continue
		}
		switch value {
		case HealthToolFailure:
			if turn.ToolFailure {
				return true
			}
		case HealthDurationSpike:
			if turn.DurationSpike {
				return true
			}
		case HealthContinuationNudge:
			if turn.ContinuationNudge {
				return true
			}
		}
	}
	return false
}

func hasName(items []TimedName, name string, start, end time.Time, bounded bool) bool {
	for _, item := range items {
		if item.Name != name {
			continue
		}
		if !bounded || inside(item.At, start, end) {
			return true
		}
	}
	return false
}

func inSpan(fact SessionFact, start, end time.Time) bool {
	if inside(fact.UpdatedAt, start, end) {
		return true
	}
	return hasTimeIn(fact.Turns, start, end) || hasTimeIn(fact.UserUtterances, start, end) || hasTimeIn(fact.AssistantUtterances, start, end)
}

func inside(at, start, end time.Time) bool {
	return !at.IsZero() && !at.Before(start) && at.Before(end)
}

func hasTimeIn(times []time.Time, start, end time.Time) bool {
	for _, at := range times {
		if inside(at, start, end) {
			return true
		}
	}
	return false
}

func countIn(times []time.Time, start, end time.Time) int {
	count := 0
	for _, at := range times {
		if inside(at, start, end) {
			count++
		}
	}
	return count
}

func addDays(report *Report, dayIndex map[string]int, loc *time.Location, fact SessionFact, start, end time.Time) {
	add := func(at time.Time, messages, turns int, tokens int64) {
		if !inside(at, start, end) {
			return
		}
		index, ok := dayIndex[at.In(loc).Format("2006-01-02")]
		if !ok {
			return
		}
		report.Days[index].Messages += messages
		report.Days[index].Turns += turns
		report.Days[index].Tokens += tokens
	}
	for _, at := range fact.UserUtterances {
		add(at, 1, 0, 0)
	}
	for _, at := range fact.AssistantUtterances {
		add(at, 1, 0, 0)
	}
	for _, at := range fact.Turns {
		add(at, 0, 1, 0)
	}
	for _, token := range fact.Tokens {
		if token.InputPresence == "missing" && token.OutputPresence == "missing" {
			continue
		}
		add(token.At, 0, 0, token.Prompt+token.CacheRead+token.CacheWrite+token.Completion)
	}
}

func addTokens(report *Report, fact SessionFact, start, end time.Time) {
	for _, token := range fact.Tokens {
		if !inside(token.At, start, end) {
			continue
		}
		if token.InputPresence == "missing" && token.OutputPresence == "missing" {
			continue
		}
		report.Summary.Tokens += token.Prompt + token.CacheRead + token.CacheWrite + token.Completion
	}
}

func addNames(counts map[string]int, items []TimedName, start, end time.Time) {
	for _, item := range items {
		if item.Name == "" || !inside(item.At, start, end) {
			continue
		}
		counts[item.Name]++
	}
}

func addHealth(health *HealthCounts, fact SessionFact, start, end time.Time) {
	tool, spike, nudge := false, false, false
	for _, turn := range fact.TurnHealth {
		if !inside(turn.At, start, end) {
			continue
		}
		tool = tool || turn.ToolFailure
		spike = spike || turn.DurationSpike
		nudge = nudge || turn.ContinuationNudge
	}
	if tool {
		health.ToolFailures++
	}
	if spike {
		health.DurationSpikes++
	}
	if nudge {
		health.ContinuationNudges++
	}
	if fact.MissingShutdown && (hasTimeIn(fact.Turns, start, end) || inside(fact.UpdatedAt, start, end)) {
		health.MissingShutdowns++
	}
}

func addCosts(costs map[string]*CostTotal, coverage *Coverage, fact SessionFact, start, end time.Time) {
	outside := activityOutside(fact, start, end)
	if outside && len(fact.Costs) > 0 {
		coverage.SpanningCostSessions++
	}
	for _, item := range fact.Costs {
		if item.Unit == "" {
			continue
		}
		precision := item.Precision
		if precision == "" {
			precision = "estimated"
		}
		if outside || item.ActivityOutside {
			precision = "estimated"
		}
		total := costs[item.Unit]
		if total == nil {
			total = &CostTotal{Unit: item.Unit, Precision: precision}
			costs[item.Unit] = total
		}
		total.Amount += item.Amount
		if precision == "estimated" {
			total.Precision = "estimated"
		}
	}
}

func activityOutside(fact SessionFact, start, end time.Time) bool {
	for _, at := range fact.Turns {
		if !at.IsZero() && !inside(at, start, end) {
			return true
		}
	}
	return false
}

func addCode(summary *Summary, fact SessionFact, start, end time.Time) bool {
	added := false
	for _, change := range fact.Code {
		if !inside(change.RecordedAt, start, end) {
			continue
		}
		summary.CodeFiles++
		if change.HasLines {
			summary.Additions += change.Additions
			summary.Deletions += change.Deletions
		}
		added = true
	}
	return added
}

func countList(counts map[string]int) []CountRow {
	rows := make([]CountRow, 0, len(counts))
	for name, count := range counts {
		rows = append(rows, CountRow{Name: name, Count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count == rows[j].Count {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Count > rows[j].Count
	})
	return rows
}

func costList(costs map[string]*CostTotal) []CostTotal {
	rows := make([]CostTotal, 0, len(costs))
	for _, total := range costs {
		rows = append(rows, *total)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Unit < rows[j].Unit })
	return rows
}

func setList(values map[string]bool) []string {
	if len(values) == 0 {
		return nil
	}
	list := make([]string, 0, len(values))
	for value := range values {
		list = append(list, value)
	}
	sort.Strings(list)
	return list
}

func card(fact SessionFact) SessionCard {
	return SessionCard{
		ID: fact.ID, AgentType: fact.AgentType, Project: fact.Project, Name: fact.Name,
		UpdatedAt: fact.UpdatedAt, Bookmarked: fact.Bookmarked,
	}
}

func sortCards(cards []SessionCard) {
	sort.Slice(cards, func(i, j int) bool { return newer(cards[i], cards[j]) })
}

func newer(left, right SessionCard) bool {
	if left.UpdatedAt.Equal(right.UpdatedAt) {
		return left.ID < right.ID
	}
	return left.UpdatedAt.After(right.UpdatedAt)
}

// AbsorbChild adds a child session's attributable usage onto its parent.
// Turns, utterances, health, and ending stay on the root session.
func AbsorbChild(parent *SessionFact, child SessionFact) {
	parent.Tokens = append(parent.Tokens, child.Tokens...)
	parent.Tools = append(parent.Tools, child.Tools...)
	parent.Skills = append(parent.Skills, child.Skills...)
	parent.Costs = append(parent.Costs, child.Costs...)
	parent.Code = append(parent.Code, child.Code...)
	parent.UntimedTokens = parent.UntimedTokens || child.UntimedTokens
	parent.UntimedSkills += child.UntimedSkills
	parent.UntimedCode += child.UntimedCode
	parent.MissingCacheRead = parent.MissingCacheRead || child.MissingCacheRead
	parent.MissingCacheWrite = parent.MissingCacheWrite || child.MissingCacheWrite
	if child.HasBill {
		parent.HasBill = true
	}
}
