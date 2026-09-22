package home

import (
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

// CodeFromEnvelope keeps file changes that carry RecordedAt. Changes without
// that timestamp are counted and omitted from the total.
func CodeFromEnvelope(envelope model.SessionGitEvidenceEnvelope) (changes []CodeChange, untimed int) {
	for _, repository := range envelope.Repositories {
		for _, file := range repository.Files {
			recordedAt := earliestRecordedAt(file)
			change := CodeChange{Path: file.DisplayPath}
			if file.Additions != nil && file.Deletions != nil {
				change.HasLines = true
				change.Additions = *file.Additions
				change.Deletions = *file.Deletions
			}
			if recordedAt.IsZero() {
				untimed++
				continue
			}
			change.RecordedAt = recordedAt
			changes = append(changes, change)
		}
	}
	return changes, untimed
}

func earliestRecordedAt(file model.GitFileChange) time.Time {
	var earliest time.Time
	for _, link := range file.Evidence {
		if link.RecordedAt == nil {
			continue
		}
		if earliest.IsZero() || link.RecordedAt.Before(earliest) {
			earliest = *link.RecordedAt
		}
	}
	return earliest
}
