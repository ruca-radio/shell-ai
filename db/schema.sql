-- Shell-AI Persistent Memory Schema
-- SQLite database schema for conversation memory with contextual recall

-- Enable foreign keys
PRAGMA foreign_keys = ON;

-- ============================================================================
-- Core Tables
-- ============================================================================

-- Sessions table: Stores conversation sessions
CREATE TABLE IF NOT EXISTS sessions (
    id              TEXT PRIMARY KEY,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    project_path    TEXT NOT NULL,
    title           TEXT,
    summary         TEXT
);

-- Messages table: Stores individual messages within sessions
CREATE TABLE IF NOT EXISTS messages (
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL,
    role            TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
    content         TEXT NOT NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    token_count     INTEGER DEFAULT 0,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
);

-- Context files table: Tracks files referenced during sessions
CREATE TABLE IF NOT EXISTS context_files (
    id              TEXT PRIMARY KEY,
    session_id      TEXT NOT NULL,
    file_path       TEXT NOT NULL,
    content_hash    TEXT NOT NULL,
    added_at        DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
    UNIQUE (session_id, file_path)
);

-- Tags table: Stores tag definitions
CREATE TABLE IF NOT EXISTS tags (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL UNIQUE
);

-- Session tags junction table: Many-to-many relationship
CREATE TABLE IF NOT EXISTS session_tags (
    session_id      TEXT NOT NULL,
    tag_id          INTEGER NOT NULL,
    PRIMARY KEY (session_id, tag_id),
    FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE,
    FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE
);

-- ============================================================================
-- Full-Text Search (FTS5)
-- ============================================================================

-- FTS5 virtual table for full-text search on message content
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    content,
    content='messages',
    content_rowid='rowid'
);

-- Triggers to keep FTS index synchronized with messages table
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, content) VALUES (NEW.rowid, NEW.content);
END;

CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, content) VALUES('delete', OLD.rowid, OLD.content);
END;

CREATE TRIGGER IF NOT EXISTS messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, content) VALUES('delete', OLD.rowid, OLD.content);
    INSERT INTO messages_fts(rowid, content) VALUES (NEW.rowid, NEW.content);
END;

-- ============================================================================
-- Indexes
-- ============================================================================

-- Fast session lookup by project_path
CREATE INDEX IF NOT EXISTS idx_sessions_project_path ON sessions(project_path);

-- Recent sessions ordering (descending by updated_at)
CREATE INDEX IF NOT EXISTS idx_sessions_updated_at ON sessions(updated_at DESC);

-- Fast message lookup by session
CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id);

-- Message ordering within session
CREATE INDEX IF NOT EXISTS idx_messages_created_at ON messages(session_id, created_at);

-- Context files lookup by session
CREATE INDEX IF NOT EXISTS idx_context_files_session_id ON context_files(session_id);

-- Tag name lookup
CREATE INDEX IF NOT EXISTS idx_tags_name ON tags(name);

-- Session tags lookup by session
CREATE INDEX IF NOT EXISTS idx_session_tags_session ON session_tags(session_id);

-- Session tags lookup by tag
CREATE INDEX IF NOT EXISTS idx_session_tags_tag ON session_tags(tag_id);

-- ============================================================================
-- Documentation Cache Tables
-- ============================================================================

-- Documentation entries table: Stores cached documentation
CREATE TABLE IF NOT EXISTS docs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL,
    source          TEXT NOT NULL,
    content         TEXT NOT NULL,
    summary         TEXT,
    version         TEXT,
    fetched_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at      DATETIME,
    UNIQUE (name, source)
);

-- Documentation FTS5 for full-text search
CREATE VIRTUAL TABLE IF NOT EXISTS docs_fts USING fts5(
    name,
    content,
    summary,
    content='docs',
    content_rowid='id'
);

-- Triggers to keep docs FTS index synchronized
CREATE TRIGGER IF NOT EXISTS docs_ai AFTER INSERT ON docs BEGIN
    INSERT INTO docs_fts(rowid, name, content, summary) VALUES (NEW.id, NEW.name, NEW.content, NEW.summary);
END;

CREATE TRIGGER IF NOT EXISTS docs_ad AFTER DELETE ON docs BEGIN
    INSERT INTO docs_fts(docs_fts, rowid, name, content, summary) VALUES('delete', OLD.id, OLD.name, OLD.content, OLD.summary);
END;

CREATE TRIGGER IF NOT EXISTS docs_au AFTER UPDATE ON docs BEGIN
    INSERT INTO docs_fts(docs_fts, rowid, name, content, summary) VALUES('delete', OLD.id, OLD.name, OLD.content, OLD.summary);
    INSERT INTO docs_fts(rowid, name, content, summary) VALUES (NEW.id, NEW.name, NEW.content, NEW.summary);
END;

-- Documentation indexes
CREATE INDEX IF NOT EXISTS idx_docs_name ON docs(name);
CREATE INDEX IF NOT EXISTS idx_docs_source ON docs(source);
CREATE INDEX IF NOT EXISTS idx_docs_expires ON docs(expires_at);

-- ============================================================================
-- Trigger for updated_at
-- ============================================================================

CREATE TRIGGER IF NOT EXISTS sessions_updated_at 
AFTER UPDATE ON sessions
BEGIN
    UPDATE sessions SET updated_at = CURRENT_TIMESTAMP WHERE id = NEW.id;
END;
