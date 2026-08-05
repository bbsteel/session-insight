package hermes

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DeleteSession mirrors Hermes' session-delete boundary: delegated child
// sessions are removed with their origin, while compression/rewind children
// that are not delegated are orphaned so their transcripts remain resumable.
func (r *HermesReader) DeleteSession(id string) error {
	if _, err := r.readSessionRow(id); err != nil {
		return err
	}
	w, err := sql.Open("sqlite3", sqliteDSN(r.dbPath, "_busy_timeout=5000&_foreign_keys=on"))
	if err != nil {
		return err
	}
	defer w.Close()
	w.SetMaxOpenConns(1)
	if err := w.Ping(); err != nil {
		return err
	}
	schema, err := loadSchema(w)
	if err != nil {
		return err
	}
	targets, err := deleteTargets(w, schema, id)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return fmt.Errorf("hermes session not found: %s", id)
	}

	tx, err := w.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	ids := sortedKeys(targets)
	placeholders := strings.TrimRight(strings.Repeat("?,", len(ids)), ",")
	args := stringsToAny(ids)

	// Preserve non-delegated descendants by clearing their parent pointer.
	if schema.hasColumn("sessions", "parent_session_id") {
		query := `UPDATE "sessions" SET "parent_session_id" = NULL WHERE "parent_session_id" IN (` + placeholders + `) AND "id" NOT IN (` + placeholders + `)`
		if _, err := tx.Exec(query, append(args, args...)...); err != nil {
			return err
		}
	}

	if schema.hasTable("async_delegations") {
		var clauses []string
		var asyncArgs []any
		for _, column := range []string{"origin_session", "parent_session_id", "origin_ui_session_id"} {
			if !schema.hasColumn("async_delegations", column) {
				continue
			}
			clauses = append(clauses, quoteIdentifier(column)+" IN ("+placeholders+")")
			asyncArgs = append(asyncArgs, args...)
		}
		if len(clauses) > 0 {
			if _, err := tx.Exec(`DELETE FROM "async_delegations" WHERE `+strings.Join(clauses, " OR "), asyncArgs...); err != nil {
				return err
			}
		}
	}

	for _, table := range []string{"messages", "session_model_usage", "compression_locks"} {
		if !schema.hasTable(table) || !schema.hasColumn(table, "session_id") {
			continue
		}
		if _, err := tx.Exec(`DELETE FROM `+quoteIdentifier(table)+` WHERE "session_id" IN (`+placeholders+")", args...); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM "sessions" WHERE "id" IN (`+placeholders+")", args...); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true

	return removeLegacyFiles(filepath.Join(filepath.Dir(r.dbPath), "sessions"), ids)
}

type deleteSessionRow struct {
	ID, ParentID, ModelConfig, Source string
}

func deleteTargets(db *sql.DB, schema schemaInfo, root string) (map[string]bool, error) {
	if !schema.hasColumn("sessions", "id") {
		return nil, fmt.Errorf("hermes sessions table has no id column")
	}
	query := `SELECT "id", `
	if schema.hasColumn("sessions", "parent_session_id") {
		query += `"parent_session_id"`
	} else {
		query += "NULL"
	}
	query += ", "
	if schema.hasColumn("sessions", "model_config") {
		query += `"model_config"`
	} else {
		query += "NULL"
	}
	query += ", "
	if schema.hasColumn("sessions", "source") {
		query += `"source"`
	} else {
		query += "NULL"
	}
	query += ` FROM "sessions"`
	rows, err := db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var all []deleteSessionRow
	for rows.Next() {
		var id, parentID, modelConfig, source sql.NullString
		if err := rows.Scan(&id, &parentID, &modelConfig, &source); err != nil {
			return nil, err
		}
		row := deleteSessionRow{ID: id.String, ParentID: parentID.String, ModelConfig: modelConfig.String, Source: source.String}
		all = append(all, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	found := false
	for _, row := range all {
		if row.ID == root {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("hermes session not found: %s", root)
	}

	targets := map[string]bool{root: true}
	changed := true
	for changed {
		changed = false
		for _, row := range all {
			if targets[row.ID] || !targets[row.ParentID] || !isDelegateDeleteRow(row) {
				continue
			}
			targets[row.ID] = true
			changed = true
		}
	}
	return targets, nil
}

func isDelegateDeleteRow(row deleteSessionRow) bool {
	if strings.EqualFold(row.Source, "tool") {
		return true
	}
	var config map[string]any
	if json.Unmarshal([]byte(row.ModelConfig), &config) != nil {
		return false
	}
	for _, key := range []string{"_delegate_from", "delegate_from"} {
		if value, ok := config[key].(string); ok && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func sortedKeys(values map[string]bool) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func stringsToAny(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

func removeLegacyFiles(dir string, ids []string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, id := range ids {
		if id == "" || filepath.Base(id) != id || strings.ContainsAny(id, `/\\`) {
			continue
		}
		for _, suffix := range []string{".json", ".jsonl"} {
			wanted[id+suffix] = true
		}
	}
	for _, entry := range entries {
		name := entry.Name()
		remove := wanted[name]
		if !remove {
			for _, id := range ids {
				if strings.HasPrefix(name, "request_dump_"+id+"_") && strings.HasSuffix(name, ".json") {
					remove = true
					break
				}
			}
		}
		if remove {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
