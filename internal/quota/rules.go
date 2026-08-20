package quota

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// JSONWindowRule describes common provider response shapes. Custom providers
// only need a small parser when their response uses arrays or a non-standard
// authentication flow; ordinary nested JSON can be handled declaratively.
type JSONWindowRule struct {
	ID                    string
	UsedPercentPath       string
	RemainingFractionPath string
	UsedAmountPath        string
	RemainingAmountPath   string
	LimitAmountPath       string
	ResetAtPath           string
	ResetAfterSecondsPath string
	WindowSecondsPath     string
	Unit                  string
}

// ParseJSONWindows applies rules to an upstream JSON object and returns only
// windows with enough data to calculate a remaining value or show a reset.
func ParseJSONWindows(payload []byte, rules []JSONWindowRule, now time.Time) ([]QuotaWindow, error) {
	var root any
	if err := json.Unmarshal(payload, &root); err != nil {
		return nil, fmt.Errorf("decode quota payload: %w", err)
	}
	windows := make([]QuotaWindow, 0, len(rules))
	for _, rule := range rules {
		window, ok, err := parseJSONWindow(root, rule, now)
		if err != nil {
			return nil, fmt.Errorf("window %q: %w", rule.ID, err)
		}
		if ok {
			windows = append(windows, window)
		}
	}
	if len(windows) == 0 {
		return nil, fmt.Errorf("quota payload contains no recognized windows")
	}
	return windows, nil
}

// ParseJSONWindowObject applies one rule to an already decoded object. It is
// useful for upstream payloads whose windows are returned in an array, while
// keeping all amount/percentage/reset calculations in the shared parser.
func ParseJSONWindowObject(object map[string]any, rule JSONWindowRule, now time.Time) (QuotaWindow, bool, error) {
	return parseJSONWindow(object, rule, now)
}

func parseJSONWindow(root any, rule JSONWindowRule, now time.Time) (QuotaWindow, bool, error) {
	window := QuotaWindow{ID: rule.ID, Unit: rule.Unit}
	valueFound := false

	if raw, ok := lookupJSONPath(root, rule.UsedPercentPath); ok {
		usedPercent, err := numberValue(raw)
		if err != nil {
			return QuotaWindow{}, false, fmt.Errorf("used percent: %w", err)
		}
		if usedPercent < 0 || usedPercent > 100 {
			return QuotaWindow{}, false, fmt.Errorf("used percent out of range: %v", usedPercent)
		}
		remainingPercent := 100 - usedPercent
		window.UsedPercent = &usedPercent
		window.RemainingPercent = &remainingPercent
		valueFound = true
	}
	if raw, ok := lookupJSONPath(root, rule.RemainingFractionPath); ok {
		remainingFraction, err := numberValue(raw)
		if err != nil {
			return QuotaWindow{}, false, fmt.Errorf("remaining fraction: %w", err)
		}
		if remainingFraction < 0 || remainingFraction > 1 {
			return QuotaWindow{}, false, fmt.Errorf("remaining fraction out of range: %v", remainingFraction)
		}
		remainingPercent := remainingFraction * 100
		usedPercent := 100 - remainingPercent
		window.RemainingPercent = &remainingPercent
		window.UsedPercent = &usedPercent
		valueFound = true
	}
	if raw, ok := lookupJSONPath(root, rule.UsedAmountPath); ok {
		usedAmount, err := numberValue(raw)
		if err != nil {
			return QuotaWindow{}, false, fmt.Errorf("used amount: %w", err)
		}
		window.UsedAmount = &usedAmount
		valueFound = true
	}
	if raw, ok := lookupJSONPath(root, rule.RemainingAmountPath); ok {
		remainingAmount, err := numberValue(raw)
		if err != nil {
			return QuotaWindow{}, false, fmt.Errorf("remaining amount: %w", err)
		}
		window.RemainingAmount = &remainingAmount
		valueFound = true
	}
	if raw, ok := lookupJSONPath(root, rule.LimitAmountPath); ok {
		limitAmount, err := numberValue(raw)
		if err != nil {
			return QuotaWindow{}, false, fmt.Errorf("limit amount: %w", err)
		}
		if limitAmount <= 0 {
			return QuotaWindow{}, false, fmt.Errorf("limit amount must be positive")
		}
		window.LimitAmount = &limitAmount
		valueFound = true
	}
	if window.RemainingPercent == nil && window.RemainingAmount != nil && window.LimitAmount != nil {
		remainingPercent := 100 * *window.RemainingAmount / *window.LimitAmount
		remainingPercent = clampPercent(remainingPercent)
		usedPercent := 100 - remainingPercent
		window.RemainingPercent = &remainingPercent
		window.UsedPercent = &usedPercent
	}
	if window.UsedPercent == nil && window.UsedAmount != nil && window.LimitAmount != nil {
		usedPercent := clampPercent(100 * *window.UsedAmount / *window.LimitAmount)
		remainingPercent := 100 - usedPercent
		window.UsedPercent = &usedPercent
		window.RemainingPercent = &remainingPercent
	}

	if raw, ok := lookupJSONPath(root, rule.ResetAtPath); ok {
		resetAt, err := timestampValue(raw)
		if err != nil {
			return QuotaWindow{}, false, fmt.Errorf("reset time: %w", err)
		}
		window.ResetAt = &resetAt
		valueFound = true
	}
	if window.ResetAt == nil {
		if raw, ok := lookupJSONPath(root, rule.ResetAfterSecondsPath); ok {
			seconds, err := numberValue(raw)
			if err != nil {
				return QuotaWindow{}, false, fmt.Errorf("reset duration: %w", err)
			}
			resetAt := now.Add(time.Duration(math.Max(0, seconds)) * time.Second)
			window.ResetAt = &resetAt
			valueFound = true
		}
	}
	if raw, ok := lookupJSONPath(root, rule.WindowSecondsPath); ok {
		seconds, err := numberValue(raw)
		if err != nil {
			return QuotaWindow{}, false, fmt.Errorf("window duration: %w", err)
		}
		wholeSeconds := int64(math.Max(0, seconds))
		window.WindowSeconds = &wholeSeconds
	}
	return window, valueFound, nil
}

func lookupJSONPath(root any, path string) (any, bool) {
	if strings.TrimSpace(path) == "" {
		return nil, false
	}
	current := root
	for _, segment := range strings.Split(path, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[segment]
		if !ok || current == nil {
			return nil, false
		}
	}
	return current, true
}

func numberValue(raw any) (float64, error) {
	switch value := raw.(type) {
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, fmt.Errorf("non-finite number")
		}
		return value, nil
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return 0, err
		}
		return parsed, nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return 0, err
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("expected number, got %T", raw)
	}
}

func timestampValue(raw any) (time.Time, error) {
	if value, ok := raw.(string); ok {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err == nil {
			return parsed, nil
		}
		if numeric, numericErr := strconv.ParseFloat(strings.TrimSpace(value), 64); numericErr == nil {
			return timestampValue(numeric)
		}
		return time.Time{}, err
	}
	seconds, err := numberValue(raw)
	if err != nil {
		return time.Time{}, err
	}
	if seconds > 1e12 {
		seconds /= 1000
	}
	return time.Unix(int64(seconds), int64((seconds-math.Floor(seconds))*float64(time.Second))).UTC(), nil
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
