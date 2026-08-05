CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    source TEXT,
    model TEXT,
    model_config TEXT,
    parent_session_id TEXT,
    started_at REAL,
    ended_at REAL,
    end_reason TEXT,
    message_count INTEGER DEFAULT 0,
    tool_call_count INTEGER DEFAULT 0,
    input_tokens INTEGER DEFAULT 0,
    output_tokens INTEGER DEFAULT 0,
    cache_read_tokens INTEGER DEFAULT 0,
    cache_write_tokens INTEGER DEFAULT 0,
    reasoning_tokens INTEGER DEFAULT 0,
    cwd TEXT,
    git_branch TEXT,
    git_repo_root TEXT,
    billing_provider TEXT,
    title TEXT,
    last_activity_at REAL,
    api_call_count INTEGER DEFAULT 0,
    archived INTEGER DEFAULT 0
);
CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT,
    role TEXT,
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
    active INTEGER DEFAULT 1
);
INSERT INTO sessions (id, source, model, started_at, ended_at, message_count, cwd, title, last_activity_at, api_call_count)
VALUES ('hermes-interrupted-001', 'cli', 'nous/hermes-4', 1767225600, NULL, 2, '/tmp/hermes-fixture/project', 'Interrupted Hermes session', 1767225605, 1);
INSERT INTO messages (session_id, role, content, timestamp)
VALUES ('hermes-interrupted-001', 'user', 'continue the sample task', 1767225600);
INSERT INTO messages (session_id, role, content, timestamp, finish_reason)
VALUES ('hermes-interrupted-001', 'assistant', 'I was still working', 1767225605, NULL);
