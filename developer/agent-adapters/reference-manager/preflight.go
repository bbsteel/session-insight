package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/bbsteel/session-insight/internal/reader"
)

const (
	ResultOK                         = "ok"
	ResultWorkOrderChanged           = "work_order_changed"
	ResultBaselineChanged            = "baseline_changed"
	ResultAgentNotRegistered         = "agent_not_registered"
	ResultNonNativeSource            = "non_native_source"
	ResultInvalidCaptureID           = "invalid_capture_id"
	ResultPrivateInputMissing        = "private_input_missing"
	ResultUnsupportedWorkOrderSchema = "unsupported_work_order_schema"
	ResultMainLockUnreadable         = "main_lock_unreadable"
)

type preflightResult struct {
	OK         bool   `json:"ok"`
	ResultCode string `json:"result_code"`
	WorkOrder  string `json:"work_order,omitempty"`
	Agent      string `json:"agent,omitempty"`
	Detail     string `json:"detail,omitempty"`
}

func writePreflight(out *preflightResult) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

func runVerifyWorkOrder(args []string) int {
	fs := flag.NewFlagSet("verify-work-order", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	workOrder := fs.String("work-order", "", "absolute path to WORK_ORDER.md")
	if err := fs.Parse(args); err != nil {
		writePreflight(&preflightResult{OK: false, ResultCode: ResultInvalidCaptureID, Detail: err.Error()})
		return 1
	}
	if strings.TrimSpace(*workOrder) == "" {
		writePreflight(&preflightResult{OK: false, ResultCode: ResultPrivateInputMissing, Detail: "missing --work-order"})
		return 1
	}
	checkoutDir, err := os.Getwd()
	if err != nil {
		writePreflight(&preflightResult{OK: false, ResultCode: ResultPrivateInputMissing, Detail: err.Error()})
		return 1
	}
	preferred, err := defaultStoreRoot()
	if err != nil {
		writePreflight(&preflightResult{OK: false, ResultCode: ResultPrivateInputMissing, Detail: err.Error()})
		return 1
	}
	root, _, err := ensureStoreRoot(preferred, checkoutFallbackStore(checkoutDir), os.Getenv(StoreRootEnv) == "")
	if err != nil {
		writePreflight(&preflightResult{OK: false, ResultCode: ResultPrivateInputMissing, Detail: err.Error()})
		return 1
	}
	store := newStore(root, func(agent string) (string, bool) {
		def, ok := reader.AgentDefinition(agent)
		if !ok {
			return "", false
		}
		return def.AgentType, true
	})
	result := preflightWorkOrderFile(store, checkoutDir, *workOrder)
	writePreflight(result)
	if result.OK {
		return 0
	}
	return 1
}

func preflightWorkOrderFile(store *Store, checkoutDir, workOrderPath string) *preflightResult {
	abs, err := filepath.Abs(workOrderPath)
	if err != nil {
		return &preflightResult{OK: false, ResultCode: ResultPrivateInputMissing, Detail: err.Error()}
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return &preflightResult{OK: false, ResultCode: ResultPrivateInputMissing, Detail: err.Error()}
	}
	agent, id, err := parseWorkOrderHeader(string(data))
	if err != nil {
		return &preflightResult{OK: false, ResultCode: ResultUnsupportedWorkOrderSchema, Detail: err.Error()}
	}
	return preflightWorkOrderID(store, checkoutDir, agent, id, abs)
}

func preflightWorkOrderID(store *Store, checkoutDir, agent, id, expectedPath string) *preflightResult {
	if agent == "imported" {
		return &preflightResult{OK: false, ResultCode: ResultNonNativeSource, Agent: agent, WorkOrder: id}
	}
	if _, ok := reader.AgentDefinition(agent); !ok {
		return &preflightResult{OK: false, ResultCode: ResultAgentNotRegistered, Agent: agent, WorkOrder: id}
	}
	cat, err := store.LoadCatalog(agent)
	if err != nil {
		return &preflightResult{OK: false, ResultCode: ResultPrivateInputMissing, Agent: agent, WorkOrder: id, Detail: err.Error()}
	}
	var record *WorkOrderRecord
	for i := range cat.WorkOrders {
		if cat.WorkOrders[i].ID == id {
			record = &cat.WorkOrders[i]
			break
		}
	}
	if record == nil {
		return &preflightResult{OK: false, ResultCode: ResultPrivateInputMissing, Agent: agent, WorkOrder: id, Detail: "work order id is not in the local catalog"}
	}
	if expectedPath != "" {
		want := filepath.Join(checkoutDir, record.Dir, "WORK_ORDER.md")
		if filepath.Clean(expectedPath) != filepath.Clean(want) && !sameFile(expectedPath, want) {
			// Allow verifying the catalog path or the resolved copy next to assets.
			if filepath.Base(expectedPath) != "WORK_ORDER.md" {
				return &preflightResult{OK: false, ResultCode: ResultPrivateInputMissing, Agent: agent, WorkOrder: id, Detail: "work-order path must be WORK_ORDER.md"}
			}
		}
	}
	return evaluatePreflight(store, checkoutDir, agent, *record)
}

func evaluatePreflight(store *Store, checkoutDir, agent string, record WorkOrderRecord) *preflightResult {
	out := &preflightResult{Agent: agent, WorkOrder: record.ID}
	if record.SchemaVersion != WorkOrderSchemaV2 {
		out.ResultCode = ResultUnsupportedWorkOrderSchema
		out.Detail = "regenerate the work order with the current Reference Manager"
		return out
	}
	if agent == "imported" {
		out.ResultCode = ResultNonNativeSource
		return out
	}
	if _, ok := reader.AgentDefinition(agent); !ok {
		out.ResultCode = ResultAgentNotRegistered
		return out
	}
	baseline, err := lookupBaseline(checkoutDir)
	if err != nil {
		out.ResultCode = ResultMainLockUnreadable
		out.Detail = err.Error()
		return out
	}
	if record.BaselineSHA != "" && record.BaselineSHA != baseline.SHA {
		out.ResultCode = ResultBaselineChanged
		out.Detail = fmt.Sprintf("work order baseline %s, local %s is %s", record.BaselineSHA, baseline.Ref, baseline.SHA)
		return out
	}
	lock, err := loadAgentLock(checkoutDir, baseline.SHA, agent)
	if err != nil {
		out.ResultCode = ResultMainLockUnreadable
		out.Detail = err.Error()
		return out
	}
	lockHashes := lockHashesByLogical(lock)
	for name, recorded := range record.MainLockHashes {
		current := lockHashFor(lockHashes, name, "")
		if normalizeSHA256(recorded) != current {
			out.ResultCode = ResultBaselineChanged
			out.Detail = fmt.Sprintf("main lock hash for %s changed", name)
			return out
		}
	}
	cat, err := store.LoadCatalog(agent)
	if err != nil {
		out.ResultCode = ResultPrivateInputMissing
		out.Detail = err.Error()
		return out
	}
	assetsDir := filepath.Join(checkoutDir, record.Dir, "selected-reference-assets")
	for name, frozen := range record.Frozen {
		if !knownLogicalNames[name] && !strings.HasPrefix(name, "19-agent-specific") {
			out.ResultCode = ResultInvalidCaptureID
			out.Detail = name
			return out
		}
		st := cat.Items[name]
		if st == nil || st.Current == nil {
			out.ResultCode = ResultPrivateInputMissing
			out.Detail = name
			return out
		}
		if st.Current.Hash != frozen {
			out.ResultCode = ResultWorkOrderChanged
			out.Detail = name
			return out
		}
		asset := findAsset(assetsDir, name, st.Current.Ext)
		if asset == "" {
			out.ResultCode = ResultPrivateInputMissing
			out.Detail = "frozen asset missing: " + name
			return out
		}
		sum, err := hashFile(asset)
		if err != nil || sum != frozen {
			out.ResultCode = ResultWorkOrderChanged
			out.Detail = name
			return out
		}
		if !store.blobExists(agent, st.Current) {
			out.ResultCode = ResultPrivateInputMissing
			out.Detail = name
			return out
		}
	}
	out.OK = true
	out.ResultCode = ResultOK
	return out
}

func findAsset(dir, logical, ext string) string {
	candidates := []string{
		filepath.Join(dir, logical+ext),
		filepath.Join(dir, logical+".png"),
	}
	for _, path := range candidates {
		if fileExists(path) {
			return path
		}
	}
	return ""
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha256Hex(data), nil
}

func sameFile(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

func parseWorkOrderHeader(md string) (agent, id string, err error) {
	for _, line := range strings.Split(md, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# Terminal reference work order:") {
			agent = strings.TrimSpace(strings.TrimPrefix(line, "# Terminal reference work order:"))
		}
		if strings.HasPrefix(line, "- Work order ID:") {
			id = strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "- Work order ID:")), "`")
		}
	}
	if agent == "" || id == "" {
		return "", "", fmt.Errorf("WORK_ORDER.md is missing agent or id")
	}
	if !strings.Contains(md, "work_order_schema_version: 2") && !strings.Contains(md, "Schema version: `2`") && !strings.Contains(md, "schema version: 2") {
		return agent, id, fmt.Errorf("unsupported work order schema")
	}
	return agent, id, nil
}
