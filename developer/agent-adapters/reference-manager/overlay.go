package main

import (
	"os"
	"path/filepath"
)

const (
	OverlayPlanned    = "planned"
	OverlayInProgress = "in_progress"
	OverlayValidated  = "validated"
	OverlayInReview   = "in_review"
	OverlayMerged     = "merged"
)

func validationReceiptPath(checkoutDir, workOrderID string) string {
	return filepath.Join(workOrderRoot(checkoutDir), workOrderID, "VALIDATION.json")
}

func prReceiptPath(checkoutDir, workOrderID string) string {
	return filepath.Join(workOrderRoot(checkoutDir), workOrderID, "PR.json")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// workOrderOverlay reports development progress without changing evidence
// status. Data sources are the ones named in the implementation design.
func workOrderOverlay(record WorkOrderRecord, cat *AgentCatalog, checkoutDir string, lockHashes map[string]string, headLockHashes map[string]string, headSHA, baselineSHA string) string {
	state := workOrderState(record, cat, lockHashes)
	if state == WorkOrderConsumed {
		return OverlayMerged
	}
	if state == WorkOrderUnsupported || state == WorkOrderStale {
		return OverlayPlanned
	}
	if fileExists(prReceiptPath(checkoutDir, record.ID)) {
		return OverlayInReview
	}
	if fileExists(validationReceiptPath(checkoutDir, record.ID)) {
		return OverlayValidated
	}
	if headSHA != "" && baselineSHA != "" && headSHA != baselineSHA && frozenMatchesLock(record, headLockHashes) {
		return OverlayInProgress
	}
	return OverlayPlanned
}

func frozenMatchesLock(record WorkOrderRecord, lockHashes map[string]string) bool {
	if len(record.Frozen) == 0 {
		return false
	}
	for name, frozen := range record.Frozen {
		if lockHashFor(lockHashes, name, "") != frozen {
			return false
		}
	}
	return true
}
