package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/bbsteel/session-insight/internal/model"
)

type ChangeHostRecord struct {
	HostID              string                      `json:"host_id"`
	Provider            model.ChangeProviderKind    `json:"provider"`
	DisplayOrigin       string                      `json:"display_origin"`
	EndpointOrigins     []string                    `json:"endpoint_origins"`
	Lifecycle           string                      `json:"lifecycle"`
	AllowHTTP           bool                        `json:"allow_http"`
	AllowPrivateNetwork bool                        `json:"allow_private_network"`
	Assessment          model.GitEvidenceAssessment `json:"assessment"`
	// CredentialReference points at the host credential (keyring:/env:). It is
	// storage-internal: never projected into API DTOs or logs.
	CredentialReference string     `json:"-"`
	ApprovedAt          *time.Time `json:"approved_at,omitempty"`
	RevokedAt           *time.Time `json:"revoked_at,omitempty"`
	LastCheckedAt       *time.Time `json:"last_checked_at,omitempty"`
}

func (db *DB) StoreChangeHostPreview(record ChangeHostRecord) error {
	if record.HostID == "" || !model.IsKnownChangeProviderKind(record.Provider) || record.Provider == model.ChangeProviderGeneric ||
		record.DisplayOrigin == "" || len(record.EndpointOrigins) == 0 {
		return fmt.Errorf("invalid Change Request host preview")
	}
	origin, err := url.Parse(record.DisplayOrigin)
	if err != nil || origin.Hostname() == "" || (origin.Scheme != "https" && origin.Scheme != "http") {
		return fmt.Errorf("invalid Change Request host origin")
	}
	port := 443
	if origin.Scheme == "http" {
		port = 80
	}
	if origin.Port() != "" {
		port, err = strconv.Atoi(origin.Port())
		if err != nil || port < 1 || port > 65535 {
			return fmt.Errorf("invalid Change Request host port")
		}
	}
	endpoints, err := json.Marshal(record.EndpointOrigins)
	if err != nil {
		return err
	}
	assessment := model.NonExactGitEvidence(model.GitEvidenceMissing, model.ReasonChangeHostNotApproved)
	stored, err := marshalAssessment(assessment)
	if err != nil {
		return err
	}
	result, err := db.conn.Exec(`
		INSERT INTO change_hosts(
			host_id, provider, scheme, hostname, port, display_origin,
			endpoint_origins_json, allow_http, allow_private_network,
			lifecycle, state, reason_code
		) VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, 'preview', ?, ?)
		ON CONFLICT(host_id) DO UPDATE SET
			provider = excluded.provider, scheme = excluded.scheme,
			hostname = excluded.hostname, port = excluded.port,
			display_origin = excluded.display_origin,
			endpoint_origins_json = excluded.endpoint_origins_json,
			state = excluded.state, reason_code = excluded.reason_code
		WHERE change_hosts.lifecycle = 'preview'`,
		record.HostID, record.Provider, origin.Scheme, origin.Hostname(), port,
		record.DisplayOrigin, string(endpoints), stored.state, stored.reasonCode,
	)
	if err != nil {
		return fmt.Errorf("store Change Request host preview: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("approved or revoked Change Request host cannot be replaced by preview")
	}
	return nil
}

func (db *DB) ApproveChangeHost(hostID string, allowHTTP, allowPrivateNetwork bool, approvedAt time.Time) error {
	result, err := db.conn.Exec(`
		UPDATE change_hosts
		SET lifecycle = 'approved', state = 'exact', reason_code = '',
		    allow_http = ?, allow_private_network = ?, approved_at = ?, revoked_at = NULL
		WHERE host_id = ? AND lifecycle = 'preview'`,
		boolInt(allowHTTP), boolInt(allowPrivateNetwork), model.FormatTime(approvedAt), hostID,
	)
	if err != nil {
		return fmt.Errorf("approve Change Request host: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("change request host preview does not exist")
	}
	return nil
}

func (db *DB) RevokeChangeHost(hostID string, revokedAt time.Time) (bool, error) {
	result, err := db.conn.Exec(`
		UPDATE change_hosts
		SET lifecycle = 'revoked', state = 'unavailable', reason_code = ?, revoked_at = ?
		WHERE host_id = ? AND lifecycle = 'approved'`,
		model.ReasonChangeHostRevoked, model.FormatTime(revokedAt), hostID,
	)
	if err != nil {
		return false, fmt.Errorf("revoke Change Request host: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (db *DB) TouchChangeHost(hostID string, checkedAt time.Time, assessment model.GitEvidenceAssessment) error {
	stored, err := marshalAssessment(assessment)
	if err != nil {
		return err
	}
	result, err := db.conn.Exec(`
		UPDATE change_hosts SET state = ?, reason_code = ?, last_checked_at = ?
		WHERE host_id = ? AND lifecycle = 'approved'`,
		stored.state, stored.reasonCode, model.FormatTime(checkedAt), hostID,
	)
	if err != nil {
		return fmt.Errorf("update Change Request host status: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("approved Change Request host does not exist")
	}
	return nil
}

// SetChangeHostCredentialReference attaches or clears (empty reference) the
// credential pointer of a non-revoked host. The reference is validated but its
// secret is never read here.
func (db *DB) SetChangeHostCredentialReference(hostID string, reference model.CredentialReference) error {
	if reference != "" {
		if _, ok := model.ParseCredentialReference(string(reference)); !ok {
			return fmt.Errorf("invalid Change Request host credential reference")
		}
	}
	result, err := db.conn.Exec(`
		UPDATE change_hosts SET credential_reference = ?
		WHERE host_id = ? AND lifecycle <> 'revoked'`,
		string(reference), hostID,
	)
	if err != nil {
		return fmt.Errorf("set Change Request host credential reference: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return fmt.Errorf("non-revoked Change Request host does not exist")
	}
	return nil
}

func (db *DB) ChangeHost(hostID string) (ChangeHostRecord, bool, error) {
	row := db.conn.QueryRow(`
		SELECT host_id, provider, display_origin, endpoint_origins_json,
		       lifecycle, allow_http, allow_private_network,
		       state, reason_code, approved_at, revoked_at, last_checked_at,
		       credential_reference
		FROM change_hosts WHERE host_id = ?`, hostID)
	record, err := scanChangeHost(row)
	if err == sql.ErrNoRows {
		return ChangeHostRecord{}, false, nil
	}
	if err != nil {
		return ChangeHostRecord{}, false, err
	}
	return record, true, nil
}

func (db *DB) ListChangeHosts() ([]ChangeHostRecord, error) {
	rows, err := db.conn.Query(`
		SELECT host_id, provider, display_origin, endpoint_origins_json,
		       lifecycle, allow_http, allow_private_network,
		       state, reason_code, approved_at, revoked_at, last_checked_at,
		       credential_reference
		FROM change_hosts ORDER BY provider, display_origin, host_id`)
	if err != nil {
		return nil, fmt.Errorf("list Change Request hosts: %w", err)
	}
	defer rows.Close()
	hosts := []ChangeHostRecord{}
	for rows.Next() {
		record, err := scanChangeHost(rows)
		if err != nil {
			return nil, err
		}
		hosts = append(hosts, record)
	}
	return hosts, rows.Err()
}

func scanChangeHost(scanner rowScanner) (ChangeHostRecord, error) {
	var record ChangeHostRecord
	var endpointsJSON, state, reason string
	var allowHTTP, allowPrivate int
	var approvedAt, revokedAt, checkedAt sql.NullString
	if err := scanner.Scan(
		&record.HostID, &record.Provider, &record.DisplayOrigin, &endpointsJSON,
		&record.Lifecycle, &allowHTTP, &allowPrivate, &state, &reason,
		&approvedAt, &revokedAt, &checkedAt, &record.CredentialReference,
	); err != nil {
		return record, err
	}
	record.AllowHTTP = allowHTTP != 0
	record.AllowPrivateNetwork = allowPrivate != 0
	if err := json.Unmarshal([]byte(endpointsJSON), &record.EndpointOrigins); err != nil {
		return record, fmt.Errorf("decode Change Request host endpoints: %w", err)
	}
	var err error
	if record.Assessment, err = decodeStoredAssessment(state, reason, assessmentReasonsJSON(state, reason)); err != nil {
		return record, err
	}
	if record.ApprovedAt, err = parseOptionalStoredTime(approvedAt); err != nil {
		return record, err
	}
	if record.RevokedAt, err = parseOptionalStoredTime(revokedAt); err != nil {
		return record, err
	}
	if record.LastCheckedAt, err = parseOptionalStoredTime(checkedAt); err != nil {
		return record, err
	}
	return record, nil
}

func assessmentReasonsJSON(state, reason string) string {
	if state == string(model.GitEvidenceExact) || reason == "" {
		return "[]"
	}
	encoded, _ := json.Marshal([]model.GitEvidenceReasonCode{model.GitEvidenceReasonCode(reason)})
	return string(encoded)
}
