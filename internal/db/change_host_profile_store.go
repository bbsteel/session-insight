package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bbsteel/session-insight/internal/changehost/openapi"
	"github.com/bbsteel/session-insight/internal/model"
)

// ErrChangeHostProfileNotFound reports a missing profile row.
var ErrChangeHostProfileNotFound = errors.New("change host profile not found")

// ErrChangeHostProfileConflict reports a lifecycle or uniqueness conflict,
// including the single-active-profile rule enforced by the database.
var ErrChangeHostProfileConflict = errors.New("change host profile conflict")

// ErrChangeHostProfileReferenced reports an attempt to delete a profile
// revision that historical snapshots still reference.
var ErrChangeHostProfileReferenced = errors.New("change host profile referenced by snapshots")

// ChangeHostProfileRecord is the persisted form of one profile revision.
// ProfileJSON is the exact validated profile document; it contains a
// credential reference but never secret material.
type ChangeHostProfileRecord struct {
	ProfileID           string                   `json:"profile_id"`
	HostID              string                   `json:"host_id"`
	ProfileRevision     int                      `json:"profile_revision"`
	SchemaVersion       int                      `json:"schema_version"`
	DisplayName         string                   `json:"display_name"`
	Lifecycle           openapi.ProfileLifecycle `json:"lifecycle"`
	ProfileJSON         string                   `json:"profile_json"`
	InferenceReportJSON string                   `json:"inference_report_json"`
	SpecDigest          string                   `json:"spec_digest"`
	SpecVersion         string                   `json:"spec_version"`
	CreatedAt           time.Time                `json:"created_at"`
	UpdatedAt           time.Time                `json:"updated_at"`
	VerifiedAt          *time.Time               `json:"verified_at,omitempty"`
	ActivatedAt         *time.Time               `json:"activated_at,omitempty"`
	LastSuccessAt       *time.Time               `json:"last_success_at,omitempty"`
	LastFailureAt       *time.Time               `json:"last_failure_at,omitempty"`
	LastFailureCode     string                   `json:"last_failure_code,omitempty"`
}

// validateProfileRecord enforces the storage-level invariants: the document
// decodes, its identity matches the row, and verified-or-later revisions pass
// the full structural contract. Drafts may still be mid-inference, so only
// their identity fields are enforced.
func validateProfileRecord(record ChangeHostProfileRecord) error {
	if record.ProfileID == "" || record.HostID == "" || record.ProfileRevision < 1 {
		return fmt.Errorf("change host profile requires an identity and revision")
	}
	if !openapi.IsKnownProfileLifecycle(record.Lifecycle) {
		return fmt.Errorf("change host profile has unknown lifecycle %q", record.Lifecycle)
	}
	profile, err := openapi.DecodeProfile([]byte(record.ProfileJSON))
	if err != nil {
		return err
	}
	if profile.SchemaVersion != record.SchemaVersion || profile.ProfileID != record.ProfileID ||
		profile.ProfileRevision != record.ProfileRevision || profile.HostID != record.HostID {
		return fmt.Errorf("change host profile document does not match its row identity")
	}
	if profile.Adapter != openapi.AdapterKind {
		return fmt.Errorf("change host profile document must use the %q adapter", openapi.AdapterKind)
	}
	if _, ok := model.ParseCredentialReference(profile.Authentication.CredentialReference); !ok {
		return fmt.Errorf("change host profile credential reference is invalid")
	}
	switch record.Lifecycle {
	case openapi.ProfileVerified, openapi.ProfileActive, openapi.ProfileDegraded:
		if issues := openapi.ValidateProfile(profile); !issues.OK() {
			return issues
		}
	}
	return nil
}

// CreateChangeHostProfileDraft inserts a new draft profile revision. Verified
// and active profiles are created through the lifecycle transitions instead.
func (db *DB) CreateChangeHostProfileDraft(record ChangeHostProfileRecord) error {
	if record.Lifecycle != openapi.ProfileDraft {
		return fmt.Errorf("new change host profiles start as draft")
	}
	if record.InferenceReportJSON == "" {
		record.InferenceReportJSON = "{}"
	}
	if err := validateProfileRecord(record); err != nil {
		return err
	}
	now := time.Now().UTC()
	record.CreatedAt = now
	record.UpdatedAt = now
	_, err := db.conn.Exec(`
		INSERT INTO change_host_profiles(
			profile_id, host_id, profile_revision, schema_version, display_name,
			lifecycle, profile_json, inference_report_json, spec_digest, spec_version,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ProfileID, record.HostID, record.ProfileRevision, record.SchemaVersion,
		record.DisplayName, string(record.Lifecycle), record.ProfileJSON,
		record.InferenceReportJSON, record.SpecDigest, record.SpecVersion,
		model.FormatTime(record.CreatedAt), model.FormatTime(record.UpdatedAt),
	)
	if err != nil {
		return fmt.Errorf("create change host profile: %w", err)
	}
	return nil
}

func (db *DB) ChangeHostProfile(profileID string) (ChangeHostProfileRecord, bool, error) {
	row := db.conn.QueryRow(changeHostProfileSelect+` WHERE profile_id = ?`, profileID)
	record, err := scanChangeHostProfile(row)
	if err == sql.ErrNoRows {
		return ChangeHostProfileRecord{}, false, nil
	}
	if err != nil {
		return ChangeHostProfileRecord{}, false, err
	}
	return record, true, nil
}

// ActiveChangeHostProfile returns the single active profile revision for one
// host, if any.
func (db *DB) ActiveChangeHostProfile(hostID string) (ChangeHostProfileRecord, bool, error) {
	row := db.conn.QueryRow(changeHostProfileSelect+` WHERE host_id = ? AND lifecycle = 'active'`, hostID)
	record, err := scanChangeHostProfile(row)
	if err == sql.ErrNoRows {
		return ChangeHostProfileRecord{}, false, nil
	}
	if err != nil {
		return ChangeHostProfileRecord{}, false, err
	}
	return record, true, nil
}

func (db *DB) ListChangeHostProfiles(hostID string) ([]ChangeHostProfileRecord, error) {
	rows, err := db.conn.Query(changeHostProfileSelect+` WHERE host_id = ? ORDER BY profile_revision DESC`, hostID)
	if err != nil {
		return nil, fmt.Errorf("list change host profiles: %w", err)
	}
	defer rows.Close()
	profiles := []ChangeHostProfileRecord{}
	for rows.Next() {
		record, err := scanChangeHostProfile(rows)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, record)
	}
	return profiles, rows.Err()
}

const changeHostProfileSelect = `
	SELECT profile_id, host_id, profile_revision, schema_version, display_name,
	       lifecycle, profile_json, inference_report_json, spec_digest, spec_version,
	       created_at, updated_at, verified_at, activated_at,
	       last_success_at, last_failure_at, last_failure_code
	FROM change_host_profiles`

// MarkChangeHostProfileVerified records a draft that passed sample-change
// verification. The profile document is re-validated at the same time so a
// draft whose mapping was edited can never skip the full contract.
func (db *DB) MarkChangeHostProfileVerified(profileID string, verifiedAt time.Time) error {
	record, exists, err := db.ChangeHostProfile(profileID)
	if err != nil {
		return err
	}
	if !exists {
		return ErrChangeHostProfileNotFound
	}
	if record.Lifecycle != openapi.ProfileDraft && record.Lifecycle != openapi.ProfileInvalid {
		return ErrChangeHostProfileConflict
	}
	if issues := openapi.ValidateProfile(mustDecodeProfile(record)); !issues.OK() {
		return issues
	}
	now := model.FormatTime(time.Now().UTC())
	result, err := db.conn.Exec(`
		UPDATE change_host_profiles
		SET lifecycle = 'verified', verified_at = ?, updated_at = ?, last_failure_code = ''
		WHERE profile_id = ? AND lifecycle IN ('draft','invalid')`,
		model.FormatTime(verifiedAt), now, profileID,
	)
	if err != nil {
		return fmt.Errorf("verify change host profile: %w", err)
	}
	return requireOneRow(result, ErrChangeHostProfileConflict)
}

// ActivateChangeHostProfile atomically makes one verified revision the active
// profile of its host. Any previously active revision is revoked in the same
// transaction so a host never has zero or two active revisions mid-switch.
// The host itself must be approved.
func (db *DB) ActivateChangeHostProfile(profileID string, activatedAt time.Time) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var hostID, lifecycle string
	err = tx.QueryRow(`SELECT host_id, lifecycle FROM change_host_profiles WHERE profile_id = ?`, profileID).
		Scan(&hostID, &lifecycle)
	if err == sql.ErrNoRows {
		return ErrChangeHostProfileNotFound
	}
	if err != nil {
		return fmt.Errorf("activate change host profile: %w", err)
	}
	if lifecycle != string(openapi.ProfileVerified) {
		return ErrChangeHostProfileConflict
	}
	var hostLifecycle string
	if err := tx.QueryRow(`SELECT lifecycle FROM change_hosts WHERE host_id = ?`, hostID).Scan(&hostLifecycle); err != nil {
		return fmt.Errorf("activate change host profile: %w", err)
	}
	if hostLifecycle != "approved" {
		return fmt.Errorf("%w: host is not approved", ErrChangeHostProfileConflict)
	}
	if _, err := tx.Exec(`
		UPDATE change_host_profiles
		SET lifecycle = 'revoked', updated_at = ?
		WHERE host_id = ? AND lifecycle = 'active'`,
		model.FormatTime(activatedAt), hostID,
	); err != nil {
		return fmt.Errorf("activate change host profile: demote previous: %w", err)
	}
	result, err := tx.Exec(`
		UPDATE change_host_profiles
		SET lifecycle = 'active', activated_at = ?, updated_at = ?
		WHERE profile_id = ? AND lifecycle = 'verified'`,
		model.FormatTime(activatedAt), model.FormatTime(activatedAt), profileID,
	)
	if err != nil {
		return fmt.Errorf("activate change host profile: %w", err)
	}
	if err := requireOneRow(result, ErrChangeHostProfileConflict); err != nil {
		return err
	}
	return tx.Commit()
}

// MarkChangeHostProfileDegraded records runtime schema drift. Only active
// profiles degrade; the mapping itself is never edited in place.
func (db *DB) MarkChangeHostProfileDegraded(profileID, failureCode string, failedAt time.Time) error {
	if failureCode == "" {
		return fmt.Errorf("degraded change host profile requires a failure code")
	}
	result, err := db.conn.Exec(`
		UPDATE change_host_profiles
		SET lifecycle = 'degraded', last_failure_at = ?, last_failure_code = ?, updated_at = ?
		WHERE profile_id = ? AND lifecycle = 'active'`,
		model.FormatTime(failedAt), failureCode, model.FormatTime(failedAt), profileID,
	)
	if err != nil {
		return fmt.Errorf("degrade change host profile: %w", err)
	}
	return requireOneRow(result, ErrChangeHostProfileConflict)
}

// RevokeChangeHostProfile takes one profile out of service. Already revoked
// rows report a conflict so callers can distinguish a no-op retry.
func (db *DB) RevokeChangeHostProfile(profileID string, revokedAt time.Time) error {
	result, err := db.conn.Exec(`
		UPDATE change_host_profiles
		SET lifecycle = 'revoked', updated_at = ?
		WHERE profile_id = ? AND lifecycle IN ('draft','verified','active','degraded','invalid')`,
		model.FormatTime(revokedAt), profileID,
	)
	if err != nil {
		return fmt.Errorf("revoke change host profile: %w", err)
	}
	return requireOneRow(result, ErrChangeHostProfileConflict)
}

// TouchChangeHostProfileSuccess records a successful runtime use of an active
// profile without affecting its lifecycle.
func (db *DB) TouchChangeHostProfileSuccess(profileID string, succeededAt time.Time) error {
	result, err := db.conn.Exec(`
		UPDATE change_host_profiles SET last_success_at = ?
		WHERE profile_id = ? AND lifecycle IN ('active','degraded')`,
		model.FormatTime(succeededAt), profileID,
	)
	if err != nil {
		return fmt.Errorf("touch change host profile success: %w", err)
	}
	return requireOneRow(result, ErrChangeHostProfileNotFound)
}

// DeleteChangeHostProfile removes one draft, invalid, or revoked revision.
// The delete-guard trigger additionally blocks removal while any historical
// snapshot references the profile.
func (db *DB) DeleteChangeHostProfile(profileID string) error {
	result, err := db.conn.Exec(`
		DELETE FROM change_host_profiles
		WHERE profile_id = ? AND lifecycle IN ('draft','invalid','revoked')`, profileID)
	if err != nil {
		if isSQLiteAbort(err, "referenced by historical snapshots") {
			return ErrChangeHostProfileReferenced
		}
		return fmt.Errorf("delete change host profile: %w", err)
	}
	return requireOneRow(result, ErrChangeHostProfileNotFound)
}

func isSQLiteAbort(err error, fragment string) bool {
	return err != nil && strings.Contains(err.Error(), fragment)
}

func requireOneRow(result sql.Result, conflict error) error {
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return conflict
	}
	return nil
}

func mustDecodeProfile(record ChangeHostProfileRecord) openapi.Profile {
	profile, err := openapi.DecodeProfile([]byte(record.ProfileJSON))
	if err != nil {
		// validateProfileRecord accepted this row, so a decode failure here
		// means storage corruption; surface an empty profile and let the
		// validator reject it.
		return openapi.Profile{}
	}
	return profile
}

func scanChangeHostProfile(scanner rowScanner) (ChangeHostProfileRecord, error) {
	var record ChangeHostProfileRecord
	var lifecycle string
	var createdAt, updatedAt string
	var verifiedAt, activatedAt, successAt, failureAt sql.NullString
	if err := scanner.Scan(
		&record.ProfileID, &record.HostID, &record.ProfileRevision, &record.SchemaVersion,
		&record.DisplayName, &lifecycle, &record.ProfileJSON, &record.InferenceReportJSON,
		&record.SpecDigest, &record.SpecVersion, &createdAt, &updatedAt,
		&verifiedAt, &activatedAt, &successAt, &failureAt, &record.LastFailureCode,
	); err != nil {
		return record, err
	}
	record.Lifecycle = openapi.ProfileLifecycle(lifecycle)
	var err error
	if record.CreatedAt, err = parseStoredTime(createdAt); err != nil {
		return record, err
	}
	if record.UpdatedAt, err = parseStoredTime(updatedAt); err != nil {
		return record, err
	}
	if record.VerifiedAt, err = parseOptionalStoredTime(verifiedAt); err != nil {
		return record, err
	}
	if record.ActivatedAt, err = parseOptionalStoredTime(activatedAt); err != nil {
		return record, err
	}
	if record.LastSuccessAt, err = parseOptionalStoredTime(successAt); err != nil {
		return record, err
	}
	if record.LastFailureAt, err = parseOptionalStoredTime(failureAt); err != nil {
		return record, err
	}
	return record, nil
}
