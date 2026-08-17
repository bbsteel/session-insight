package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"unicode"
)

type v34SchemaObject struct {
	kind string
	name string
	ddl  string
}

var v34SchemaObjects = []v34SchemaObject{
	{kind: "table", name: "session_git_bindings", ddl: `CREATE TABLE session_git_bindings (
		binding_id TEXT PRIMARY KEY,
		agent_type TEXT NOT NULL,
		session_id TEXT NOT NULL,
		repository_entry_key TEXT NOT NULL,
		worktree_root TEXT NOT NULL,
		common_root_id TEXT NOT NULL,
		worktree_id TEXT NOT NULL,
		branch TEXT NOT NULL DEFAULT '',
		head_sha TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL CHECK (state IN ('exact','estimated','missing','unavailable')),
		reason_code TEXT NOT NULL DEFAULT '',
		reasons_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(reasons_json)),
		observed_at TEXT NOT NULL,
		UNIQUE (agent_type, session_id, repository_entry_key),
		UNIQUE (binding_id, agent_type, session_id),
		FOREIGN KEY (agent_type, session_id) REFERENCES sessions(agent_type, id) ON DELETE CASCADE,
		CHECK (head_sha = '' OR ((length(head_sha) = 40 OR length(head_sha) = 64) AND head_sha NOT GLOB '*[^0-9a-f]*'))
	)`},
	{kind: "table", name: "session_git_origins", ddl: `CREATE TABLE session_git_origins (
		binding_id TEXT PRIMARY KEY,
		source_revision TEXT NOT NULL DEFAULT '',
		origin_json TEXT NOT NULL CHECK (json_valid(origin_json)),
		captured_at TEXT NOT NULL,
		FOREIGN KEY (binding_id) REFERENCES session_git_bindings(binding_id) ON DELETE CASCADE
	)`},
	{kind: "table", name: "session_git_snapshots", ddl: `CREATE TABLE session_git_snapshots (
		snapshot_id TEXT PRIMARY KEY,
		binding_id TEXT NOT NULL,
		kind TEXT NOT NULL CHECK (kind IN ('baseline','checkpoint','final')),
		source_revision TEXT NOT NULL,
		manifest_digest TEXT NOT NULL DEFAULT '',
		head_sha TEXT NOT NULL DEFAULT '',
		index_fingerprint TEXT NOT NULL DEFAULT '',
		status_fingerprint TEXT NOT NULL DEFAULT '',
		state TEXT NOT NULL CHECK (state IN ('exact','estimated','missing','unavailable')),
		reason_code TEXT NOT NULL DEFAULT '',
		reasons_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(reasons_json)),
		provisional INTEGER NOT NULL DEFAULT 0 CHECK (provisional IN (0,1)),
		capture_started_at TEXT NOT NULL,
		capture_completed_at TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		UNIQUE (binding_id, kind, source_revision, manifest_digest),
		UNIQUE (binding_id, snapshot_id),
		UNIQUE (binding_id, snapshot_id, kind),
		FOREIGN KEY (binding_id) REFERENCES session_git_bindings(binding_id) ON DELETE CASCADE,
		CHECK (head_sha = '' OR ((length(head_sha) = 40 OR length(head_sha) = 64) AND head_sha NOT GLOB '*[^0-9a-f]*')),
		CHECK (capture_completed_at >= capture_started_at)
	)`},
	{kind: "table", name: "session_git_snapshot_files", ddl: `CREATE TABLE session_git_snapshot_files (
		snapshot_id TEXT NOT NULL,
		path_key TEXT NOT NULL,
		ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
		raw_path BLOB NOT NULL,
		display_path TEXT NOT NULL,
		path_encoding TEXT NOT NULL CHECK (path_encoding IN ('utf8','bytes_b64')),
		layer TEXT NOT NULL CHECK (layer IN ('tree','index','worktree','hosted_change')),
		file_type TEXT NOT NULL DEFAULT 'file' CHECK (file_type IN ('file','symlink','submodule','binary')),
		mode TEXT NOT NULL DEFAULT '',
		git_oid TEXT NOT NULL DEFAULT '',
		content_hash TEXT NOT NULL DEFAULT '',
		content_bytes INTEGER NOT NULL DEFAULT 0 CHECK (content_bytes >= 0),
		content_state TEXT NOT NULL CHECK (content_state IN ('exact','estimated','missing','unavailable')),
		reason_code TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (snapshot_id, path_key),
		UNIQUE (snapshot_id, ordinal),
		FOREIGN KEY (snapshot_id) REFERENCES session_git_snapshots(snapshot_id) ON DELETE CASCADE,
		CHECK (length(path_key) = 64 AND path_key NOT GLOB '*[^0-9a-f]*'),
		CHECK (git_oid = '' OR ((length(git_oid) = 40 OR length(git_oid) = 64) AND git_oid NOT GLOB '*[^0-9a-f]*')),
		CHECK (content_hash = '' OR (length(content_hash) = 64 AND content_hash NOT GLOB '*[^0-9a-f]*'))
	)`},
	{kind: "table", name: "change_hosts", ddl: `CREATE TABLE change_hosts (
		host_id TEXT PRIMARY KEY,
		provider TEXT NOT NULL CHECK (provider IN ('github','gitlab','gitea','forgejo','bitbucket_cloud','bitbucket_data_center','azure_devops','gerrit','generic')),
		scheme TEXT NOT NULL DEFAULT 'https' CHECK (scheme IN ('https','http')),
		hostname TEXT NOT NULL,
		port INTEGER NOT NULL DEFAULT 443 CHECK (port BETWEEN 1 AND 65535),
		display_origin TEXT NOT NULL,
		endpoint_origins_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(endpoint_origins_json)),
		credential_reference TEXT NOT NULL DEFAULT '',
		allow_http INTEGER NOT NULL DEFAULT 0 CHECK (allow_http IN (0,1)),
		allow_private_network INTEGER NOT NULL DEFAULT 0 CHECK (allow_private_network IN (0,1)),
		lifecycle TEXT NOT NULL CHECK (lifecycle IN ('preview','approved','revoked')),
		state TEXT NOT NULL CHECK (state IN ('exact','estimated','missing','unavailable')),
		reason_code TEXT NOT NULL DEFAULT '',
		approved_at TEXT,
		revoked_at TEXT,
		last_checked_at TEXT,
		UNIQUE (scheme, hostname, port),
		UNIQUE (host_id, provider),
		CHECK (hostname <> '' AND display_origin <> ''),
		CHECK (json_type(endpoint_origins_json) = 'array'),
		CHECK (lifecycle = 'preview' OR json_array_length(endpoint_origins_json) > 0),
		CHECK (scheme = 'https' OR lifecycle = 'preview' OR allow_http = 1),
		CHECK (provider <> 'generic' OR lifecycle = 'preview'),
		CHECK ((lifecycle = 'preview' AND approved_at IS NULL AND revoked_at IS NULL) OR (lifecycle = 'approved' AND approved_at IS NOT NULL AND revoked_at IS NULL) OR (lifecycle = 'revoked' AND approved_at IS NOT NULL AND revoked_at IS NOT NULL))
	)`},
	{kind: "table", name: "hosted_repositories", ddl: `CREATE TABLE hosted_repositories (
		repository_id TEXT PRIMARY KEY,
		host_id TEXT NOT NULL,
		provider_immutable_id TEXT NOT NULL,
		slug TEXT NOT NULL,
		sanitized_remote TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		UNIQUE (host_id, provider_immutable_id),
		UNIQUE (repository_id, host_id),
		FOREIGN KEY (host_id) REFERENCES change_hosts(host_id) ON DELETE RESTRICT
	)`},
	{kind: "table", name: "change_request_identities", ddl: `CREATE TABLE change_request_identities (
		change_id TEXT PRIMARY KEY,
		provider TEXT NOT NULL CHECK (provider IN ('github','gitlab','gitea','forgejo','bitbucket_cloud','bitbucket_data_center','azure_devops','gerrit','generic')),
		host_id TEXT,
		target_repository_id TEXT,
		provider_object_id TEXT NOT NULL DEFAULT '',
		generic_opaque_id TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		UNIQUE (change_id, host_id),
		FOREIGN KEY (host_id) REFERENCES change_hosts(host_id) ON DELETE RESTRICT,
		FOREIGN KEY (host_id, provider) REFERENCES change_hosts(host_id, provider) ON DELETE RESTRICT,
		FOREIGN KEY (target_repository_id, host_id) REFERENCES hosted_repositories(repository_id, host_id) ON DELETE RESTRICT,
		CHECK ((provider = 'generic' AND generic_opaque_id <> '' AND host_id IS NULL AND target_repository_id IS NULL AND provider_object_id = '') OR (provider <> 'generic' AND generic_opaque_id = '' AND host_id IS NOT NULL AND target_repository_id IS NOT NULL AND provider_object_id <> ''))
	)`},
	{kind: "table", name: "change_request_snapshots", ddl: `CREATE TABLE change_request_snapshots (
		snapshot_id TEXT PRIMARY KEY,
		change_id TEXT NOT NULL,
		content_version_key TEXT NOT NULL,
		native_version TEXT NOT NULL DEFAULT '',
		metadata_revision TEXT NOT NULL,
		base_ref_sha TEXT NOT NULL DEFAULT '',
		diff_base_sha TEXT NOT NULL DEFAULT '',
		head_sha TEXT NOT NULL DEFAULT '',
		file_manifest_digest TEXT NOT NULL DEFAULT '',
		kind TEXT NOT NULL CHECK (kind IN ('pull_request','merge_request','change','code_review')),
		display_number TEXT NOT NULL,
		lifecycle_state TEXT NOT NULL CHECK (lifecycle_state IN ('open','merged','closed','abandoned','unknown')),
		draft INTEGER NOT NULL DEFAULT 0 CHECK (draft IN (0,1)),
		title TEXT NOT NULL DEFAULT '',
		web_url TEXT NOT NULL,
		source_repository_id TEXT,
		source_ref TEXT NOT NULL DEFAULT '',
		target_ref TEXT NOT NULL DEFAULT '',
		merge_commit_sha TEXT NOT NULL DEFAULT '',
		squash_commit_sha TEXT NOT NULL DEFAULT '',
		completeness_json TEXT NOT NULL CHECK (json_valid(completeness_json)),
		etag TEXT NOT NULL DEFAULT '',
		fetched_at TEXT NOT NULL,
		cache_state TEXT NOT NULL DEFAULT 'current' CHECK (cache_state IN ('current','stale','content_deleted')),
		UNIQUE (change_id, content_version_key),
		UNIQUE (change_id, snapshot_id),
		UNIQUE (change_id, snapshot_id, content_version_key),
		FOREIGN KEY (change_id) REFERENCES change_request_identities(change_id) ON DELETE CASCADE,
		FOREIGN KEY (source_repository_id) REFERENCES hosted_repositories(repository_id) ON DELETE RESTRICT
	)`},
	{kind: "table", name: "change_request_files", ddl: `CREATE TABLE change_request_files (
		snapshot_id TEXT NOT NULL,
		file_key TEXT NOT NULL,
		ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
		layer TEXT NOT NULL DEFAULT 'hosted_change' CHECK (layer = 'hosted_change'),
		display_path TEXT NOT NULL,
		old_display_path TEXT NOT NULL DEFAULT '',
		path_bytes_b64 TEXT NOT NULL DEFAULT '',
		old_path_bytes_b64 TEXT NOT NULL DEFAULT '',
		path_encoding TEXT NOT NULL CHECK (path_encoding IN ('utf8','bytes_b64')),
		status TEXT NOT NULL CHECK (status IN ('added','modified','deleted','renamed','copied')),
		old_mode TEXT NOT NULL DEFAULT '',
		new_mode TEXT NOT NULL DEFAULT '',
		binary INTEGER NOT NULL DEFAULT 0 CHECK (binary IN (0,1)),
		submodule INTEGER NOT NULL DEFAULT 0 CHECK (submodule IN (0,1)),
		additions INTEGER CHECK (additions IS NULL OR additions >= 0),
		deletions INTEGER CHECK (deletions IS NULL OR deletions >= 0),
		status_state TEXT NOT NULL CHECK (status_state IN ('exact','estimated','missing','unavailable')),
		status_reason_code TEXT NOT NULL DEFAULT '',
		status_reasons_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(status_reasons_json)),
		patch_state TEXT NOT NULL CHECK (patch_state IN ('exact','estimated','missing','unavailable')),
		patch_reason_code TEXT NOT NULL DEFAULT '',
		patch_reasons_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(patch_reasons_json)),
		PRIMARY KEY (snapshot_id, file_key),
		UNIQUE (snapshot_id, ordinal),
		FOREIGN KEY (snapshot_id) REFERENCES change_request_snapshots(snapshot_id) ON DELETE CASCADE
	)`},
	{kind: "table", name: "change_request_commits", ddl: `CREATE TABLE change_request_commits (
		snapshot_id TEXT NOT NULL,
		sha TEXT NOT NULL,
		ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
		subject TEXT NOT NULL,
		author_name TEXT NOT NULL DEFAULT '',
		authored_at TEXT,
		committed_at TEXT,
		relation TEXT NOT NULL CHECK (relation IN ('descendant','change_request_membership','path_overlap','time_window')),
		state TEXT NOT NULL CHECK (state IN ('exact','estimated','missing','unavailable')),
		reason_code TEXT NOT NULL DEFAULT '',
		reasons_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(reasons_json)),
		PRIMARY KEY (snapshot_id, sha),
		UNIQUE (snapshot_id, ordinal),
		FOREIGN KEY (snapshot_id) REFERENCES change_request_snapshots(snapshot_id) ON DELETE CASCADE,
		CHECK ((length(sha) = 40 OR length(sha) = 64) AND sha NOT GLOB '*[^0-9a-f]*')
	)`},
	{kind: "table", name: "change_request_cache_heads", ddl: `CREATE TABLE change_request_cache_heads (
		change_id TEXT PRIMARY KEY,
		snapshot_id TEXT NOT NULL,
		last_sync_at TEXT,
		next_sync_at TEXT,
		state TEXT NOT NULL CHECK (state IN ('current','stale','unavailable')),
		reason_code TEXT NOT NULL DEFAULT '',
		FOREIGN KEY (change_id, snapshot_id) REFERENCES change_request_snapshots(change_id, snapshot_id) ON DELETE RESTRICT
	)`},
	{kind: "table", name: "session_git_evidence", ddl: `CREATE TABLE session_git_evidence (
		evidence_id TEXT PRIMARY KEY,
		binding_id TEXT NOT NULL UNIQUE,
		revision INTEGER NOT NULL CHECK (revision >= 1),
		state TEXT NOT NULL CHECK (state IN ('exact','estimated','missing','unavailable')),
		reason_code TEXT NOT NULL DEFAULT '',
		reasons_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(reasons_json)),
		provisional INTEGER NOT NULL DEFAULT 0 CHECK (provisional IN (0,1)),
		stale INTEGER NOT NULL DEFAULT 0 CHECK (stale IN (0,1)),
		authority TEXT NOT NULL CHECK (authority IN ('hosted_change','local_interval','commit_graph','none')),
		baseline_snapshot_id TEXT,
		final_snapshot_id TEXT,
		selected_change_snapshot_id TEXT,
		authority_selection_json TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(authority_selection_json)),
		generated_at TEXT NOT NULL,
		FOREIGN KEY (binding_id) REFERENCES session_git_bindings(binding_id) ON DELETE CASCADE,
		FOREIGN KEY (baseline_snapshot_id) REFERENCES session_git_snapshots(snapshot_id) ON DELETE SET NULL,
		FOREIGN KEY (final_snapshot_id) REFERENCES session_git_snapshots(snapshot_id) ON DELETE SET NULL,
		FOREIGN KEY (selected_change_snapshot_id) REFERENCES change_request_snapshots(snapshot_id) ON DELETE RESTRICT
	)`},
	{kind: "table", name: "session_git_files", ddl: `CREATE TABLE session_git_files (
		evidence_id TEXT NOT NULL,
		file_key TEXT NOT NULL,
		ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
		layer TEXT NOT NULL CHECK (layer IN ('tree','index','worktree','hosted_change')),
		display_path TEXT NOT NULL,
		old_display_path TEXT NOT NULL DEFAULT '',
		path_bytes_b64 TEXT NOT NULL DEFAULT '',
		old_path_bytes_b64 TEXT NOT NULL DEFAULT '',
		path_encoding TEXT NOT NULL CHECK (path_encoding IN ('utf8','bytes_b64')),
		status TEXT NOT NULL CHECK (status IN ('added','modified','deleted','renamed','copied')),
		old_mode TEXT NOT NULL DEFAULT '',
		new_mode TEXT NOT NULL DEFAULT '',
		binary INTEGER NOT NULL DEFAULT 0 CHECK (binary IN (0,1)),
		submodule INTEGER NOT NULL DEFAULT 0 CHECK (submodule IN (0,1)),
		additions INTEGER CHECK (additions IS NULL OR additions >= 0),
		deletions INTEGER CHECK (deletions IS NULL OR deletions >= 0),
		status_state TEXT NOT NULL CHECK (status_state IN ('exact','estimated','missing','unavailable')),
		status_reason_code TEXT NOT NULL DEFAULT '',
		status_reasons_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(status_reasons_json)),
		patch_state TEXT NOT NULL CHECK (patch_state IN ('exact','estimated','missing','unavailable')),
		patch_reason_code TEXT NOT NULL DEFAULT '',
		patch_reasons_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(patch_reasons_json)),
		PRIMARY KEY (evidence_id, file_key),
		UNIQUE (evidence_id, ordinal),
		FOREIGN KEY (evidence_id) REFERENCES session_git_evidence(evidence_id) ON DELETE CASCADE
	)`},
	{kind: "table", name: "session_git_candidate_commits", ddl: `CREATE TABLE session_git_candidate_commits (
		evidence_id TEXT NOT NULL,
		sha TEXT NOT NULL,
		ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
		subject TEXT NOT NULL,
		author_name TEXT NOT NULL DEFAULT '',
		authored_at TEXT,
		committed_at TEXT,
		relation TEXT NOT NULL CHECK (relation IN ('descendant','change_request_membership','path_overlap','time_window')),
		state TEXT NOT NULL CHECK (state IN ('exact','estimated','missing','unavailable')),
		reason_code TEXT NOT NULL DEFAULT '',
		reasons_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(reasons_json)),
		PRIMARY KEY (evidence_id, sha),
		UNIQUE (evidence_id, ordinal),
		FOREIGN KEY (evidence_id) REFERENCES session_git_evidence(evidence_id) ON DELETE CASCADE,
		CHECK ((length(sha) = 40 OR length(sha) = 64) AND sha NOT GLOB '*[^0-9a-f]*')
	)`},
	{kind: "table", name: "session_git_evidence_links", ddl: `CREATE TABLE session_git_evidence_links (
		link_id INTEGER PRIMARY KEY AUTOINCREMENT,
		evidence_id TEXT NOT NULL,
		file_key TEXT,
		commit_sha TEXT,
		ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
		root_agent_type TEXT NOT NULL,
		root_session_id TEXT NOT NULL,
		source_agent_type TEXT NOT NULL,
		source_session_id TEXT NOT NULL,
		backing_agent_type TEXT NOT NULL DEFAULT '',
		backing_session_id TEXT NOT NULL DEFAULT '',
		invocation_id TEXT NOT NULL DEFAULT '',
		source_revision TEXT NOT NULL,
		positions_revision INTEGER NOT NULL CHECK (positions_revision >= 1),
		event_id TEXT NOT NULL DEFAULT '',
		tool_call_id TEXT NOT NULL DEFAULT '',
		turn_index INTEGER CHECK (turn_index IS NULL OR turn_index >= 0),
		recorded_at TEXT,
		state TEXT NOT NULL CHECK (state IN ('exact','estimated','missing','unavailable')),
		reason_code TEXT NOT NULL DEFAULT '',
		reasons_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(reasons_json)),
		FOREIGN KEY (evidence_id, file_key) REFERENCES session_git_files(evidence_id, file_key) ON DELETE CASCADE,
		FOREIGN KEY (evidence_id, commit_sha) REFERENCES session_git_candidate_commits(evidence_id, sha) ON DELETE CASCADE,
		CHECK ((file_key IS NOT NULL) + (commit_sha IS NOT NULL) = 1),
		UNIQUE (evidence_id, file_key, ordinal),
		UNIQUE (evidence_id, commit_sha, ordinal)
	)`},
	{kind: "table", name: "session_change_requests", ddl: `CREATE TABLE session_change_requests (
		link_id TEXT PRIMARY KEY,
		ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
		root_agent_type TEXT NOT NULL,
		root_session_id TEXT NOT NULL,
		source_agent_type TEXT NOT NULL,
		source_session_id TEXT NOT NULL,
		collaboration_revision INTEGER NOT NULL CHECK (collaboration_revision >= 1),
		invocation_id TEXT NOT NULL DEFAULT '',
		binding_id TEXT,
		change_id TEXT NOT NULL,
		snapshot_id TEXT,
		content_version_key TEXT NOT NULL DEFAULT '',
		relationship TEXT NOT NULL CHECK (relationship IN ('exclusive','contributing','related')),
		method TEXT NOT NULL CHECK (method IN ('explicit','agent_native','url_mention','head_sha','commit_membership','branch')),
		state TEXT NOT NULL CHECK (state IN ('exact','estimated','missing','unavailable')),
		reason_code TEXT NOT NULL DEFAULT '',
		reasons_json TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(reasons_json)),
		confirmation_source TEXT NOT NULL CHECK (confirmation_source IN ('none','user')),
		confirmation_revision TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (root_agent_type, root_session_id) REFERENCES sessions(agent_type, id) ON DELETE CASCADE,
		FOREIGN KEY (binding_id, root_agent_type, root_session_id) REFERENCES session_git_bindings(binding_id, agent_type, session_id) ON DELETE CASCADE,
		FOREIGN KEY (change_id) REFERENCES change_request_identities(change_id) ON DELETE RESTRICT,
		FOREIGN KEY (change_id, snapshot_id, content_version_key) REFERENCES change_request_snapshots(change_id, snapshot_id, content_version_key) ON DELETE RESTRICT,
		CHECK (snapshot_id IS NULL OR content_version_key <> ''),
		CHECK (relationship = 'related' OR (binding_id IS NOT NULL AND snapshot_id IS NOT NULL AND content_version_key <> '')),
		CHECK (relationship <> 'exclusive' OR (snapshot_id IS NOT NULL AND method = 'explicit' AND confirmation_source = 'user' AND confirmation_revision <> '')),
		CHECK ((confirmation_source = 'none' AND confirmation_revision = '') OR (confirmation_source = 'user' AND confirmation_revision <> ''))
	)`},
	{kind: "table", name: "change_request_aliases", ddl: `CREATE TABLE change_request_aliases (
		alias_id INTEGER PRIMARY KEY AUTOINCREMENT,
		alias_kind TEXT NOT NULL CHECK (alias_kind IN ('url','branch','head_sha','display_number','provider_native')),
		host_id TEXT,
		repository_id TEXT,
		alias_value TEXT NOT NULL,
		change_id TEXT NOT NULL,
		snapshot_id TEXT,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		expires_at TEXT,
		FOREIGN KEY (repository_id, host_id) REFERENCES hosted_repositories(repository_id, host_id) ON DELETE CASCADE,
		FOREIGN KEY (change_id) REFERENCES change_request_identities(change_id) ON DELETE CASCADE,
		FOREIGN KEY (change_id, snapshot_id) REFERENCES change_request_snapshots(change_id, snapshot_id) ON DELETE CASCADE
	)`},
	{kind: "table", name: "source_content_blobs", ddl: `CREATE TABLE source_content_blobs (
		sha256 TEXT PRIMARY KEY,
		content BLOB NOT NULL,
		raw_bytes INTEGER NOT NULL CHECK (raw_bytes >= 0),
		stored_bytes INTEGER NOT NULL CHECK (stored_bytes >= 0),
		codec TEXT NOT NULL DEFAULT 'raw' CHECK (codec IN ('raw')),
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		last_accessed_at TEXT NOT NULL DEFAULT (datetime('now')),
		CHECK (length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'),
		CHECK (codec <> 'raw' OR length(content) = stored_bytes)
	)`},
	{kind: "table", name: "source_content_blob_refs", ddl: `CREATE TABLE source_content_blob_refs (
		ref_id INTEGER PRIMARY KEY AUTOINCREMENT,
		blob_sha TEXT NOT NULL,
		local_snapshot_id TEXT,
		evidence_id TEXT,
		change_snapshot_id TEXT,
		path_key TEXT NOT NULL,
		purpose TEXT NOT NULL CHECK (purpose IN ('before','after','patch','manifest')),
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		FOREIGN KEY (blob_sha) REFERENCES source_content_blobs(sha256) ON DELETE RESTRICT,
		FOREIGN KEY (local_snapshot_id) REFERENCES session_git_snapshots(snapshot_id) ON DELETE CASCADE,
		FOREIGN KEY (evidence_id) REFERENCES session_git_evidence(evidence_id) ON DELETE CASCADE,
		FOREIGN KEY (change_snapshot_id) REFERENCES change_request_snapshots(snapshot_id) ON DELETE CASCADE,
		CHECK ((local_snapshot_id IS NOT NULL) + (evidence_id IS NOT NULL) + (change_snapshot_id IS NOT NULL) = 1)
	)`},
	{kind: "trigger", name: "trg_change_hosts_endpoint_insert", ddl: `CREATE TRIGGER trg_change_hosts_endpoint_insert
		BEFORE INSERT ON change_hosts
		WHEN NEW.lifecycle <> 'preview' AND NOT EXISTS (
			SELECT 1 FROM json_each(NEW.endpoint_origins_json)
			WHERE json_each.type = 'text' AND json_each.value = NEW.display_origin
		)
		BEGIN
			SELECT RAISE(ABORT, 'approved change host must include its display origin');
		END`},
	{kind: "trigger", name: "trg_change_hosts_endpoint_update", ddl: `CREATE TRIGGER trg_change_hosts_endpoint_update
		BEFORE UPDATE ON change_hosts
		WHEN NEW.lifecycle <> 'preview' AND NOT EXISTS (
			SELECT 1 FROM json_each(NEW.endpoint_origins_json)
			WHERE json_each.type = 'text' AND json_each.value = NEW.display_origin
		)
		BEGIN
			SELECT RAISE(ABORT, 'approved change host must include its display origin');
		END`},
	{kind: "trigger", name: "trg_change_hosts_approval_immutable", ddl: `CREATE TRIGGER trg_change_hosts_approval_immutable
		BEFORE UPDATE ON change_hosts
		WHEN (OLD.lifecycle = 'preview' AND NEW.lifecycle = 'revoked') OR
			(OLD.lifecycle = 'approved' AND NEW.lifecycle = 'preview') OR
			(OLD.lifecycle = 'revoked' AND NEW.lifecycle <> 'revoked') OR
			(OLD.lifecycle <> 'preview' AND (
				NEW.provider IS NOT OLD.provider OR NEW.scheme IS NOT OLD.scheme OR
				NEW.hostname IS NOT OLD.hostname OR NEW.port IS NOT OLD.port OR
				NEW.display_origin IS NOT OLD.display_origin OR
				NEW.endpoint_origins_json IS NOT OLD.endpoint_origins_json OR
				NEW.allow_http IS NOT OLD.allow_http OR
				NEW.allow_private_network IS NOT OLD.allow_private_network
			))
		BEGIN
			SELECT RAISE(ABORT, 'approved change host authority is immutable');
		END`},
	{kind: "trigger", name: "trg_hosted_repositories_identity_immutable", ddl: `CREATE TRIGGER trg_hosted_repositories_identity_immutable
		BEFORE UPDATE ON hosted_repositories
		WHEN NEW.host_id IS NOT OLD.host_id OR NEW.provider_immutable_id IS NOT OLD.provider_immutable_id
		BEGIN
			SELECT RAISE(ABORT, 'hosted repository identity is immutable');
		END`},
	{kind: "trigger", name: "trg_change_request_identities_immutable", ddl: `CREATE TRIGGER trg_change_request_identities_immutable
		BEFORE UPDATE ON change_request_identities
		WHEN NEW.provider IS NOT OLD.provider OR NEW.host_id IS NOT OLD.host_id OR NEW.target_repository_id IS NOT OLD.target_repository_id OR NEW.provider_object_id IS NOT OLD.provider_object_id OR NEW.generic_opaque_id IS NOT OLD.generic_opaque_id
		BEGIN
			SELECT RAISE(ABORT, 'change request identity is immutable');
		END`},
	{kind: "trigger", name: "trg_change_request_snapshots_identity_immutable", ddl: `CREATE TRIGGER trg_change_request_snapshots_identity_immutable
		BEFORE UPDATE ON change_request_snapshots
		WHEN NEW.change_id IS NOT OLD.change_id OR NEW.content_version_key IS NOT OLD.content_version_key OR
			NEW.native_version IS NOT OLD.native_version OR NEW.base_ref_sha IS NOT OLD.base_ref_sha OR
			NEW.diff_base_sha IS NOT OLD.diff_base_sha OR NEW.head_sha IS NOT OLD.head_sha OR
			NEW.file_manifest_digest IS NOT OLD.file_manifest_digest OR NEW.kind IS NOT OLD.kind OR
			NEW.source_repository_id IS NOT OLD.source_repository_id
		BEGIN
			SELECT RAISE(ABORT, 'change request snapshot content identity is immutable');
		END`},
	{kind: "trigger", name: "trg_change_request_snapshots_source_insert", ddl: `CREATE TRIGGER trg_change_request_snapshots_source_insert
		BEFORE INSERT ON change_request_snapshots
		WHEN NEW.source_repository_id IS NOT NULL AND NOT EXISTS (
			SELECT 1
			FROM change_request_identities identity
			JOIN hosted_repositories source ON source.repository_id = NEW.source_repository_id AND source.host_id = identity.host_id
			WHERE identity.change_id = NEW.change_id AND identity.provider <> 'generic'
		)
		BEGIN
			SELECT RAISE(ABORT, 'change request source repository must use the change host');
		END`},
	{kind: "trigger", name: "trg_change_request_snapshots_source_update", ddl: `CREATE TRIGGER trg_change_request_snapshots_source_update
		BEFORE UPDATE OF change_id, source_repository_id ON change_request_snapshots
		WHEN NEW.source_repository_id IS NOT NULL AND NOT EXISTS (
			SELECT 1
			FROM change_request_identities identity
			JOIN hosted_repositories source ON source.repository_id = NEW.source_repository_id AND source.host_id = identity.host_id
			WHERE identity.change_id = NEW.change_id AND identity.provider <> 'generic'
		)
		BEGIN
			SELECT RAISE(ABORT, 'change request source repository must use the change host');
		END`},
	{kind: "trigger", name: "trg_session_git_evidence_snapshots_insert", ddl: `CREATE TRIGGER trg_session_git_evidence_snapshots_insert
		BEFORE INSERT ON session_git_evidence
		WHEN (NEW.baseline_snapshot_id IS NOT NULL AND NOT EXISTS (
			SELECT 1 FROM session_git_snapshots snapshot
			WHERE snapshot.snapshot_id = NEW.baseline_snapshot_id AND snapshot.binding_id = NEW.binding_id AND snapshot.kind = 'baseline'
		)) OR (NEW.final_snapshot_id IS NOT NULL AND NOT EXISTS (
			SELECT 1 FROM session_git_snapshots snapshot
			WHERE snapshot.snapshot_id = NEW.final_snapshot_id AND snapshot.binding_id = NEW.binding_id AND snapshot.kind = 'final'
		))
		BEGIN
			SELECT RAISE(ABORT, 'Git evidence snapshots must match their binding and kind');
		END`},
	{kind: "trigger", name: "trg_session_git_snapshots_identity_immutable", ddl: `CREATE TRIGGER trg_session_git_snapshots_identity_immutable
		BEFORE UPDATE ON session_git_snapshots
		WHEN NEW.binding_id IS NOT OLD.binding_id OR NEW.kind IS NOT OLD.kind
		BEGIN
			SELECT RAISE(ABORT, 'Git snapshot binding and kind are immutable');
		END`},
	{kind: "trigger", name: "trg_session_git_evidence_snapshots_update", ddl: `CREATE TRIGGER trg_session_git_evidence_snapshots_update
		BEFORE UPDATE OF binding_id, baseline_snapshot_id, final_snapshot_id ON session_git_evidence
		WHEN (NEW.baseline_snapshot_id IS NOT NULL AND NOT EXISTS (
			SELECT 1 FROM session_git_snapshots snapshot
			WHERE snapshot.snapshot_id = NEW.baseline_snapshot_id AND snapshot.binding_id = NEW.binding_id AND snapshot.kind = 'baseline'
		)) OR (NEW.final_snapshot_id IS NOT NULL AND NOT EXISTS (
			SELECT 1 FROM session_git_snapshots snapshot
			WHERE snapshot.snapshot_id = NEW.final_snapshot_id AND snapshot.binding_id = NEW.binding_id AND snapshot.kind = 'final'
		))
		BEGIN
			SELECT RAISE(ABORT, 'Git evidence snapshots must match their binding and kind');
		END`},
	{kind: "trigger", name: "trg_change_request_aliases_scope_insert", ddl: `CREATE TRIGGER trg_change_request_aliases_scope_insert
		BEFORE INSERT ON change_request_aliases
		WHEN NOT EXISTS (
			SELECT 1 FROM change_request_identities identity
			WHERE identity.change_id = NEW.change_id AND (
				(identity.provider = 'generic' AND NEW.host_id IS NULL AND NEW.repository_id IS NULL) OR
				(identity.provider <> 'generic' AND NEW.host_id = identity.host_id AND NEW.repository_id = identity.target_repository_id)
			)
		)
		BEGIN
			SELECT RAISE(ABORT, 'change request alias scope must match its identity');
		END`},
	{kind: "trigger", name: "trg_change_request_aliases_scope_update", ddl: `CREATE TRIGGER trg_change_request_aliases_scope_update
		BEFORE UPDATE OF host_id, repository_id, change_id ON change_request_aliases
		WHEN NOT EXISTS (
			SELECT 1 FROM change_request_identities identity
			WHERE identity.change_id = NEW.change_id AND (
				(identity.provider = 'generic' AND NEW.host_id IS NULL AND NEW.repository_id IS NULL) OR
				(identity.provider <> 'generic' AND NEW.host_id = identity.host_id AND NEW.repository_id = identity.target_repository_id)
			)
		)
		BEGIN
			SELECT RAISE(ABORT, 'change request alias scope must match its identity');
		END`},

	{kind: "index", name: "idx_session_git_bindings_session", ddl: `CREATE INDEX idx_session_git_bindings_session ON session_git_bindings(agent_type, session_id)`},
	{kind: "index", name: "idx_session_git_bindings_worktree", ddl: `CREATE INDEX idx_session_git_bindings_worktree ON session_git_bindings(worktree_id, common_root_id)`},
	{kind: "index", name: "idx_session_git_snapshots_latest", ddl: `CREATE INDEX idx_session_git_snapshots_latest ON session_git_snapshots(binding_id, kind, capture_completed_at DESC)`},
	{kind: "index", name: "idx_session_git_snapshots_baseline", ddl: `CREATE UNIQUE INDEX idx_session_git_snapshots_baseline ON session_git_snapshots(binding_id) WHERE kind = 'baseline'`},
	{kind: "index", name: "idx_session_git_snapshot_files_hash", ddl: `CREATE INDEX idx_session_git_snapshot_files_hash ON session_git_snapshot_files(content_hash)`},
	{kind: "index", name: "idx_change_request_identity_provider", ddl: `CREATE UNIQUE INDEX idx_change_request_identity_provider ON change_request_identities(host_id, target_repository_id, provider_object_id) WHERE provider <> 'generic'`},
	{kind: "index", name: "idx_change_request_identity_generic", ddl: `CREATE UNIQUE INDEX idx_change_request_identity_generic ON change_request_identities(generic_opaque_id) WHERE provider = 'generic'`},
	{kind: "index", name: "idx_change_request_snapshots_fetched", ddl: `CREATE INDEX idx_change_request_snapshots_fetched ON change_request_snapshots(change_id, fetched_at DESC)`},
	{kind: "index", name: "idx_change_request_commits_sha", ddl: `CREATE INDEX idx_change_request_commits_sha ON change_request_commits(sha, snapshot_id)`},
	{kind: "index", name: "idx_session_git_evidence_revision", ddl: `CREATE INDEX idx_session_git_evidence_revision ON session_git_evidence(binding_id, revision)`},
	{kind: "index", name: "idx_session_change_requests_root", ddl: `CREATE INDEX idx_session_change_requests_root ON session_change_requests(root_agent_type, root_session_id, ordinal)`},
	{kind: "index", name: "idx_session_change_requests_change", ddl: `CREATE INDEX idx_session_change_requests_change ON session_change_requests(change_id, snapshot_id)`},
	{kind: "index", name: "idx_session_change_requests_exclusive_change", ddl: `CREATE UNIQUE INDEX idx_session_change_requests_exclusive_change ON session_change_requests(change_id, content_version_key) WHERE relationship = 'exclusive'`},
	{kind: "index", name: "idx_session_change_requests_exclusive_binding", ddl: `CREATE UNIQUE INDEX idx_session_change_requests_exclusive_binding ON session_change_requests(root_agent_type, root_session_id, binding_id) WHERE relationship = 'exclusive'`},
	{kind: "index", name: "idx_change_request_alias_lookup", ddl: `CREATE INDEX idx_change_request_alias_lookup ON change_request_aliases(host_id, repository_id, alias_kind, alias_value)`},
	{kind: "index", name: "idx_change_request_alias_unique", ddl: `CREATE UNIQUE INDEX idx_change_request_alias_unique ON change_request_aliases(alias_kind, COALESCE(host_id,''), COALESCE(repository_id,''), alias_value, change_id, COALESCE(snapshot_id,''))`},
	{kind: "index", name: "idx_change_request_alias_expiry", ddl: `CREATE INDEX idx_change_request_alias_expiry ON change_request_aliases(expires_at)`},
	{kind: "index", name: "idx_source_content_blob_refs_blob", ddl: `CREATE INDEX idx_source_content_blob_refs_blob ON source_content_blob_refs(blob_sha)`},
	{kind: "index", name: "idx_source_content_blob_refs_local", ddl: `CREATE UNIQUE INDEX idx_source_content_blob_refs_local ON source_content_blob_refs(local_snapshot_id, path_key, purpose) WHERE local_snapshot_id IS NOT NULL`},
	{kind: "index", name: "idx_source_content_blob_refs_evidence", ddl: `CREATE UNIQUE INDEX idx_source_content_blob_refs_evidence ON source_content_blob_refs(evidence_id, path_key, purpose) WHERE evidence_id IS NOT NULL`},
	{kind: "index", name: "idx_source_content_blob_refs_change", ddl: `CREATE UNIQUE INDEX idx_source_content_blob_refs_change ON source_content_blob_refs(change_snapshot_id, path_key, purpose) WHERE change_snapshot_id IS NOT NULL`},
}

// migrateGitAssociationV34 is intentionally independent of maxVersion. A
// version row cannot prove that every physical table and index survived an
// interrupted or pre-release migration.
func migrateGitAssociationV34(conn *sql.DB) (retErr error) {
	ctx := context.Background()
	complete, err := inspectV34Schema(ctx, conn)
	if err != nil {
		return err
	}
	versioned, err := schemaVersionExists(ctx, conn, 34)
	if err != nil {
		return fmt.Errorf("v34 inspect version: %w", err)
	}
	if complete && versioned {
		return nil
	}

	c, err := conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("v34 pin connection: %w", err)
	}
	defer c.Close()
	if _, err := c.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("v34 begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = c.ExecContext(ctx, `ROLLBACK`)
		}
	}()

	complete, err = inspectV34Schema(ctx, c)
	if err != nil {
		return err
	}
	if !complete {
		for _, object := range v34SchemaObjects {
			exists, err := schemaObjectExists(ctx, c, object)
			if err != nil {
				return fmt.Errorf("v34 inspect %s %s: %w", object.kind, object.name, err)
			}
			if exists {
				continue
			}
			if _, err := c.ExecContext(ctx, object.ddl); err != nil {
				return fmt.Errorf("v34 create %s %s: %w", object.kind, object.name, err)
			}
		}
	}
	if complete, err = inspectV34Schema(ctx, c); err != nil {
		return err
	}
	if !complete {
		return fmt.Errorf("v34 physical schema remained incomplete after migration")
	}
	if _, err := c.ExecContext(ctx, `INSERT OR IGNORE INTO schema_migrations(version) VALUES (34)`); err != nil {
		return fmt.Errorf("v34 record version: %w", err)
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		return fmt.Errorf("v34 commit: %w", err)
	}
	committed = true
	return nil
}

type schemaQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func inspectV34Schema(ctx context.Context, q schemaQueryer) (bool, error) {
	complete := true
	for _, object := range v34SchemaObjects {
		var actual string
		err := q.QueryRowContext(ctx,
			`SELECT sql FROM sqlite_master WHERE type = ? AND name = ?`,
			object.kind, object.name,
		).Scan(&actual)
		if err == sql.ErrNoRows {
			complete = false
			continue
		}
		if err != nil {
			return false, fmt.Errorf("v34 inspect %s %s: %w", object.kind, object.name, err)
		}
		if compactDDL(actual) != compactDDL(object.ddl) && !schemaObjectMatchesCompatibleSuccessor(actual, object) {
			return false, fmt.Errorf("v34 incompatible %s %s", object.kind, object.name)
		}
	}
	return complete, nil
}

func schemaObjectExists(ctx context.Context, q schemaQueryer, object v34SchemaObject) (bool, error) {
	var actual string
	err := q.QueryRowContext(ctx,
		`SELECT sql FROM sqlite_master WHERE type = ? AND name = ?`,
		object.kind, object.name,
	).Scan(&actual)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if compactDDL(actual) != compactDDL(object.ddl) && !schemaObjectMatchesCompatibleSuccessor(actual, object) {
		return false, fmt.Errorf("incompatible %s %s", object.kind, object.name)
	}
	return true, nil
}

func schemaObjectMatchesCompatibleSuccessor(actual string, object v34SchemaObject) bool {
	if object.kind == "table" && object.name == v36SnapshotFilesObject.name &&
		compactDDL(actual) == compactDDL(v36SnapshotFilesObject.ddl) {
		return true
	}
	if object.kind == "table" && object.name == v40CreationEvidenceObject.name &&
		compactDDL(actual) == compactDDL(v40CreationEvidenceObject.ddl) {
		return true
	}
	if object.kind != "trigger" {
		return false
	}
	for _, successor := range v35ReplacementTriggers {
		if successor.name == object.name && compactDDL(actual) == compactDDL(successor.ddl) {
			return true
		}
	}
	return false
}

func schemaVersionExists(ctx context.Context, q schemaQueryer, version int) (bool, error) {
	var found int
	err := q.QueryRowContext(ctx,
		`SELECT 1 FROM schema_migrations WHERE version = ?`, version,
	).Scan(&found)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func compactDDL(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "if not exists", "")
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == ';' || r == '`' || r == '"' || r == '[' || r == ']' {
			return -1
		}
		return r
	}, value)
}
