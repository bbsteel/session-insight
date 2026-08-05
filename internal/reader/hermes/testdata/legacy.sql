CREATE TABLE sessions (
    id TEXT PRIMARY KEY,
    source TEXT,
    model TEXT,
    started_at REAL,
    ended_at REAL,
    message_count INTEGER DEFAULT 0,
    input_tokens INTEGER DEFAULT 0,
    output_tokens INTEGER DEFAULT 0
);
CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id TEXT,
    role TEXT,
    content TEXT,
    tool_call_id TEXT,
    tool_calls TEXT,
    tool_name TEXT,
    timestamp REAL,
    token_count INTEGER,
    finish_reason TEXT,
    reasoning TEXT
);
INSERT INTO sessions (id, source, model, started_at, ended_at, message_count, input_tokens, output_tokens)
VALUES ('hermes-legacy-001', 'cli', 'hermes-legacy-model', 1767225600, 1767225660, 2, 9, 4);
INSERT INTO messages (session_id, role, content, timestamp)
VALUES ('hermes-legacy-001', 'user', 'read the legacy fixture', 1767225600);
INSERT INTO messages (session_id, role, content, timestamp, finish_reason)
VALUES ('hermes-legacy-001', 'assistant', 'legacy replay works', 1767225660, 'stop');
