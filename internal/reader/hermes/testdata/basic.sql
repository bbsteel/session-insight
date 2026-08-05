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
INSERT INTO sessions (
    id, source, model, started_at, ended_at, end_reason, message_count,
    input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
    reasoning_tokens, cwd, git_branch, git_repo_root, billing_provider,
    title, last_activity_at, api_call_count
) VALUES (
    'hermes-basic-001', 'cli', 'nous/hermes-4', 1767225600, 1767225660,
    'completed', 2, 12, 8, 3, 2, 1, '/tmp/hermes-fixture/project',
    'main', '/tmp/hermes-fixture/project', 'nous', 'Basic Hermes session',
    1767225660, 1
);
INSERT INTO messages (session_id, role, content, timestamp, finish_reason, reasoning)
VALUES ('hermes-basic-001', 'user', 'inspect the sample project', 1767225600, NULL, NULL);
INSERT INTO messages (session_id, role, content, timestamp, finish_reason, reasoning)
VALUES ('hermes-basic-001', 'assistant', 'I found the sample project.', 1767225660, 'stop', 'brief review');
