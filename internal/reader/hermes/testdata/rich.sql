CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT 'cli',
    model TEXT,
    model_config TEXT,
    parent_session_id TEXT,
    started_at REAL,
    ended_at REAL,
    end_reason TEXT,
    message_count INTEGER NOT NULL DEFAULT 0,
    tool_call_count INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_tokens INTEGER NOT NULL DEFAULT 0,
    cwd TEXT,
    git_branch TEXT,
    git_repo_root TEXT,
    billing_provider TEXT,
    base_url TEXT,
    mode TEXT,
    estimated_cost_usd REAL,
    actual_cost_usd REAL,
    cost_status TEXT,
    title TEXT,
    last_activity_at REAL,
    description TEXT,
    api_call_count INTEGER NOT NULL DEFAULT 0,
    profile_name TEXT,
    archived INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT NOT NULL,
    role TEXT NOT NULL,
    content TEXT,
    tool_call_id TEXT,
    tool_calls TEXT,
    tool_name TEXT,
    effect_disposition TEXT,
    timestamp REAL,
    token_count INTEGER,
    finish_reason TEXT,
    reasoning TEXT,
    reasoning_content TEXT,
    active INTEGER NOT NULL DEFAULT 1,
    compacted INTEGER NOT NULL DEFAULT 0,
    display_kind TEXT
);
CREATE TABLE session_model_usage (
    session_id TEXT NOT NULL,
    model TEXT,
    provider TEXT,
    mode TEXT,
    task TEXT,
    api_call_count INTEGER,
    input_tokens INTEGER,
    output_tokens INTEGER,
    cache_read_tokens INTEGER,
    cache_write_tokens INTEGER,
    reasoning_tokens INTEGER,
    estimated_cost_usd REAL,
    actual_cost_usd REAL,
    cost_status TEXT
);
CREATE TABLE async_delegations (
    delegation_id TEXT PRIMARY KEY,
    origin_session TEXT,
    origin_ui_session_id TEXT,
    parent_session_id TEXT,
    state TEXT,
    event_json TEXT
);
INSERT INTO sessions (
    id, source, model, model_config, started_at, ended_at, end_reason, message_count,
    tool_call_count, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
    reasoning_tokens, cwd, git_branch, git_repo_root, billing_provider, title,
    last_activity_at, api_call_count
) VALUES (
    'hermes-rich-001', 'cli', 'nous/hermes-4', '{}', 1767225600, 1767225800,
    'completed', 8, 3, 100, 50, 11, 3, 7, '/tmp/hermes-fixture/project',
    'main', '/tmp/hermes-fixture/project', 'nous', 'Rich Hermes session',
    1767225800, 3
);
INSERT INTO messages (session_id, role, content, timestamp)
VALUES ('hermes-rich-001', 'user', 'update the sample file', 1767225600);
INSERT INTO messages (session_id, role, content, tool_calls, timestamp, finish_reason)
VALUES (
    'hermes-rich-001', 'assistant', 'I will update and test it.',
    '[{"id":"call-patch-1","type":"function","function":{"name":"patch","arguments":"{\"path\":\"/tmp/hermes-fixture/project/a.go\",\"old_string\":\"old\",\"new_string\":\"new\"}"}},{"id":"call-terminal-1","type":"function","function":{"name":"terminal","arguments":"{\"command\":\"go test ./...\"}"}}]',
    1767225610, 'tool_calls'
);
INSERT INTO messages (session_id, role, content, tool_call_id, tool_name, effect_disposition, timestamp)
VALUES ('hermes-rich-001', 'tool', '{"success":true,"output":"patched"}', 'call-patch-1', 'patch', 'applied', 1767225620);
INSERT INTO messages (session_id, role, content, tool_call_id, tool_name, timestamp)
VALUES ('hermes-rich-001', 'tool', '{"output":"tests failed","exit_code":1,"error":"fixture test failure"}', 'call-terminal-1', 'terminal', 1767225630);
INSERT INTO messages (session_id, role, content, timestamp, finish_reason)
VALUES ('hermes-rich-001', 'assistant', 'The file changed; the sample test needs attention.', 1767225640, 'stop');
INSERT INTO messages (session_id, role, content, timestamp)
VALUES ('hermes-rich-001', 'user', 'delegate a review', 1767225700);
INSERT INTO messages (session_id, role, content, tool_calls, timestamp, finish_reason)
VALUES (
    'hermes-rich-001', 'assistant', 'I am delegating a review.',
    '[{"id":"call-delegate-1","type":"function","function":{"name":"delegate_task","arguments":"{\"goal\":\"review the sample change\"}"}}]',
    1767225710, 'tool_calls'
);
INSERT INTO messages (session_id, role, content, tool_call_id, tool_name, timestamp)
VALUES ('hermes-rich-001', 'tool', '{"status":"completed","output":"review complete"}', 'call-delegate-1', 'delegate_task', 1767225720);
INSERT INTO sessions (
    id, source, model, model_config, parent_session_id, started_at, ended_at,
    end_reason, message_count, input_tokens, output_tokens, cwd, title,
    last_activity_at, api_call_count
) VALUES (
    'hermes-child-001', 'tool', 'nous/hermes-4', '{"_delegate_from":"hermes-rich-001"}',
    'hermes-rich-001', 1767225711, 1767225750, 'completed', 2, 20, 10,
    '/tmp/hermes-fixture/project', 'Delegated review', 1767225750, 1
);
INSERT INTO messages (session_id, role, content, timestamp)
VALUES ('hermes-child-001', 'user', 'review the sample change', 1767225711);
INSERT INTO messages (session_id, role, content, timestamp, finish_reason)
VALUES ('hermes-child-001', 'assistant', 'The change is reviewable.', 1767225750, 'stop');
INSERT INTO session_model_usage (
    session_id, model, provider, mode, task, api_call_count, input_tokens,
    output_tokens, cache_read_tokens, cache_write_tokens, reasoning_tokens
) VALUES (
    'hermes-rich-001', 'nous/hermes-4', 'nous', 'chat', 'main', 3, 100, 50, 11, 3, 7
);
INSERT INTO async_delegations (delegation_id, origin_session, parent_session_id, state)
VALUES ('delegation-rich-001', 'hermes-rich-001', 'hermes-rich-001', 'completed');
