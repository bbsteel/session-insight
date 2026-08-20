package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Evidence status of one logical screenshot file. The states are independent
// per logical file; an Agent CLI version change never moves them in bulk.
const (
	StatusMissing         = "missing"          // no candidate session and no capture
	StatusFound           = "found"            // candidate session known, not captured yet
	StatusCaptured        = "captured"         // capture exists, not used by the current presentation
	StatusUsed            = "used"             // accepted content version matches the current capture
	StatusUpdateAvailable = "update_available" // a newer capture exists while acceptance points at older content
	StatusNotApplicable   = "not_applicable"   // adapter research proved the scene does not exist
)

// CaptureRecord is one imported screenshot for a logical file.
type CaptureRecord struct {
	Hash         string `json:"hash"` // sha256 hex of the image content
	Ext          string `json:"ext"`  // e.g. ".png"
	OriginalName string `json:"original_name,omitempty"`
	ImportedAt   string `json:"imported_at"` // RFC3339
	// ObservedVersion records which Agent CLI version the capture was
	// observed with. Context only; never an invalidation signal.
	ObservedVersion string `json:"observed_version,omitempty"`
}

// ItemState is the catalog record of one logical screenshot file.
type ItemState struct {
	Current *CaptureRecord `json:"current,omitempty"`
	// AcceptedHash / AcceptedExt identify the content version the current
	// presentation was built from. The accepted blob stays in the store so a
	// newer candidate image never destroys the implementation basis.
	AcceptedHash string `json:"accepted_hash,omitempty"`
	AcceptedExt  string `json:"accepted_ext,omitempty"`
	// NotApplicable is set only by an explicit human/adapter-research
	// decision with a reason; it is never derived from a missing image.
	NotApplicable       bool   `json:"not_applicable,omitempty"`
	NotApplicableReason string `json:"not_applicable_reason,omitempty"`
}

// WorkOrderRecord tracks one generated work order and its frozen input hashes
// so the manager can refuse stale work orders when an input changes.
type WorkOrderRecord struct {
	ID        string            `json:"id"`
	Dir       string            `json:"dir"` // checkout-relative .runtime/reference-work/<id>
	CreatedAt string            `json:"created_at"`
	Items     []string          `json:"items"`    // logical names included
	Features  []string          `json:"features"` // mapped presentation features
	Frozen    map[string]string `json:"frozen"`   // logical name -> frozen content hash
}

// AgentCatalog is the local, never-committed per-Agent reference catalog.
type AgentCatalog struct {
	Agent string `json:"agent"`
	// CurrentVersion is the installed Agent CLI version, shown as context
	// next to the observed versions of captures.
	CurrentVersion string                `json:"current_version,omitempty"`
	Items          map[string]*ItemState `json:"items"`
	WorkOrders     []WorkOrderRecord     `json:"work_orders,omitempty"`
}

func newAgentCatalog(agent string) *AgentCatalog {
	return &AgentCatalog{Agent: agent, Items: map[string]*ItemState{}}
}

func (c *AgentCatalog) item(logicalName string) *ItemState {
	st, ok := c.Items[logicalName]
	if !ok {
		st = &ItemState{}
		c.Items[logicalName] = st
	}
	return st
}

// ItemStatus is the resolved view of one logical file for the UI.
type ItemStatus struct {
	Status string `json:"status"`
	// LocalUnavailable is true when the catalog references content whose
	// blob is missing from the local store. Already-accepted presentation
	// work stays valid; this is a local-data hint only.
	LocalUnavailable bool `json:"local_unavailable,omitempty"`
}

// resolveStatus derives the evidence state of one logical file.
// hasCandidate reports whether candidate discovery found a session for it;
// blobExists reports whether the current capture's blob is on disk.
func resolveStatus(st *ItemState, hasCandidate bool, blobExists bool) ItemStatus {
	if st == nil {
		if hasCandidate {
			return ItemStatus{Status: StatusFound}
		}
		return ItemStatus{Status: StatusMissing}
	}
	if st.NotApplicable {
		return ItemStatus{Status: StatusNotApplicable}
	}
	if st.Current == nil {
		if hasCandidate {
			return ItemStatus{Status: StatusFound}
		}
		return ItemStatus{Status: StatusMissing}
	}
	out := ItemStatus{}
	if !blobExists {
		out.LocalUnavailable = true
	}
	switch st.AcceptedHash {
	case "":
		out.Status = StatusCaptured
	case st.Current.Hash:
		out.Status = StatusUsed
	default:
		out.Status = StatusUpdateAvailable
	}
	return out
}

// catalogStore loads and saves per-Agent catalogs under the reference store
// root. All writes are serialized; files are rewritten atomically.
type catalogStore struct {
	root string
	mu   sync.Mutex
}

func (s *catalogStore) catalogPath(agent string) string {
	return filepath.Join(s.root, agent, "catalog.json")
}

// load returns the catalog for agent, or an empty one when none exists.
func (s *catalogStore) load(agent string) (*AgentCatalog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked(agent)
}

func (s *catalogStore) loadLocked(agent string) (*AgentCatalog, error) {
	data, err := os.ReadFile(s.catalogPath(agent))
	if err != nil {
		if os.IsNotExist(err) {
			return newAgentCatalog(agent), nil
		}
		return nil, err
	}
	cat := newAgentCatalog(agent)
	if err := json.Unmarshal(data, cat); err != nil {
		return nil, fmt.Errorf("catalog for %s is corrupt: %w", agent, err)
	}
	if cat.Items == nil {
		cat.Items = map[string]*ItemState{}
	}
	cat.Agent = agent
	return cat, nil
}

// update loads the catalog, applies fn and persists the result atomically.
func (s *catalogStore) update(agent string, fn func(*AgentCatalog) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cat, err := s.loadLocked(agent)
	if err != nil {
		return err
	}
	if err := fn(cat); err != nil {
		return err
	}
	return s.saveLocked(cat)
}

func (s *catalogStore) saveLocked(cat *AgentCatalog) error {
	if err := os.MkdirAll(filepath.Join(s.root, cat.Agent), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cat, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.catalogPath(cat.Agent) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.catalogPath(cat.Agent))
}

// nowRFC3339 is wrapped so tests can stub time.
var nowRFC3339 = func() string { return time.Now().UTC().Format(time.RFC3339) }
