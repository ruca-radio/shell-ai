package db

import (
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

type DB struct {
	conn *sql.DB
}

func getDBPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dbDir := filepath.Join(homeDir, ".shell-ai")
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return "", err
	}
	return filepath.Join(dbDir, "memory.db"), nil
}

func Open() (*DB, error) {
	dbPath, err := getDBPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get database path: %w", err)
	}

	conn, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if _, err := conn.Exec("PRAGMA foreign_keys = ON"); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	if _, err := conn.Exec(schemaSQL); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return &DB{conn: conn}, nil
}

func (db *DB) Close() error {
	return db.conn.Close()
}

func (db *DB) CreateSession(projectPath string) (*Session, error) {
	id := uuid.New().String()
	now := time.Now()

	_, err := db.conn.Exec(
		"INSERT INTO sessions (id, project_path, created_at, updated_at) VALUES (?, ?, ?, ?)",
		id, projectPath, now, now,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}

	return &Session{
		ID:          id,
		ProjectPath: projectPath,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func (db *DB) GetSession(id string) (*Session, error) {
	row := db.conn.QueryRow(
		"SELECT id, created_at, updated_at, project_path, title, summary FROM sessions WHERE id = ?",
		id,
	)

	var s Session
	err := row.Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt, &s.ProjectPath, &s.Title, &s.Summary)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	return &s, nil
}

func (db *DB) GetRecentSessions(projectPath string, limit int) ([]SessionSummary, error) {
	query := `
		SELECT s.id, s.project_path, s.title, s.updated_at, COUNT(m.id) as message_count
		FROM sessions s
		LEFT JOIN messages m ON s.id = m.session_id
		WHERE s.project_path = ?
		GROUP BY s.id
		ORDER BY s.updated_at DESC
		LIMIT ?
	`
	rows, err := db.conn.Query(query, projectPath, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent sessions: %w", err)
	}
	defer rows.Close()

	var sessions []SessionSummary
	for rows.Next() {
		var s SessionSummary
		var title sql.NullString
		if err := rows.Scan(&s.ID, &s.ProjectPath, &title, &s.UpdatedAt, &s.MessageCount); err != nil {
			return nil, err
		}
		if title.Valid {
			s.Title = title.String
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (db *DB) UpdateSessionTitle(id string, title string) error {
	_, err := db.conn.Exec("UPDATE sessions SET title = ? WHERE id = ?", title, id)
	return err
}

func (db *DB) UpdateSessionSummary(id string, summary string) error {
	_, err := db.conn.Exec("UPDATE sessions SET summary = ? WHERE id = ?", summary, id)
	return err
}

func (db *DB) AddMessage(sessionID string, role string, content string, tokenCount int) (*Message, error) {
	id := uuid.New().String()
	now := time.Now()

	_, err := db.conn.Exec(
		"INSERT INTO messages (id, session_id, role, content, created_at, token_count) VALUES (?, ?, ?, ?, ?, ?)",
		id, sessionID, role, content, now, tokenCount,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to add message: %w", err)
	}

	return &Message{
		ID:         id,
		SessionID:  sessionID,
		Role:       role,
		Content:    content,
		CreatedAt:  now,
		TokenCount: tokenCount,
	}, nil
}

func (db *DB) GetMessages(sessionID string) ([]Message, error) {
	rows, err := db.conn.Query(
		"SELECT id, session_id, role, content, created_at, token_count FROM messages WHERE session_id = ? ORDER BY created_at",
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.CreatedAt, &m.TokenCount); err != nil {
			return nil, err
		}
		messages = append(messages, m)
	}
	return messages, nil
}

func (db *DB) SearchMessages(query string, limit int) ([]SearchResult, error) {
	rows, err := db.conn.Query(`
		SELECT m.id, m.session_id, m.content, bm25(messages_fts) as rank
		FROM messages_fts
		JOIN messages m ON messages_fts.rowid = m.rowid
		WHERE messages_fts MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search messages: %w", err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.MessageID, &r.SessionID, &r.Content, &r.Rank); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

func (db *DB) AddContextFile(sessionID string, filePath string, content string) (*ContextFile, error) {
	id := uuid.New().String()
	now := time.Now()

	hash := sha256.Sum256([]byte(content))
	contentHash := hex.EncodeToString(hash[:])

	_, err := db.conn.Exec(
		"INSERT OR REPLACE INTO context_files (id, session_id, file_path, content_hash, added_at) VALUES (?, ?, ?, ?, ?)",
		id, sessionID, filePath, contentHash, now,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to add context file: %w", err)
	}

	return &ContextFile{
		ID:          id,
		SessionID:   sessionID,
		FilePath:    filePath,
		ContentHash: contentHash,
		AddedAt:     now,
	}, nil
}

func (db *DB) GetContextFiles(sessionID string) ([]ContextFile, error) {
	rows, err := db.conn.Query(
		"SELECT id, session_id, file_path, content_hash, added_at FROM context_files WHERE session_id = ?",
		sessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get context files: %w", err)
	}
	defer rows.Close()

	var files []ContextFile
	for rows.Next() {
		var f ContextFile
		if err := rows.Scan(&f.ID, &f.SessionID, &f.FilePath, &f.ContentHash, &f.AddedAt); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

func (db *DB) AddTag(name string) (*Tag, error) {
	result, err := db.conn.Exec("INSERT OR IGNORE INTO tags (name) VALUES (?)", name)
	if err != nil {
		return nil, fmt.Errorf("failed to add tag: %w", err)
	}

	id, _ := result.LastInsertId()
	if id == 0 {
		row := db.conn.QueryRow("SELECT id FROM tags WHERE name = ?", name)
		if err := row.Scan(&id); err != nil {
			return nil, err
		}
	}

	return &Tag{ID: id, Name: name}, nil
}

func (db *DB) TagSession(sessionID string, tagName string) error {
	tag, err := db.AddTag(tagName)
	if err != nil {
		return err
	}

	_, err = db.conn.Exec(
		"INSERT OR IGNORE INTO session_tags (session_id, tag_id) VALUES (?, ?)",
		sessionID, tag.ID,
	)
	return err
}

func (db *DB) GetSessionsByTag(tagName string, limit int) ([]SessionSummary, error) {
	query := `
		SELECT s.id, s.project_path, s.title, s.updated_at, COUNT(m.id) as message_count
		FROM sessions s
		JOIN session_tags st ON s.id = st.session_id
		JOIN tags t ON st.tag_id = t.id
		LEFT JOIN messages m ON s.id = m.session_id
		WHERE t.name = ?
		GROUP BY s.id
		ORDER BY s.updated_at DESC
		LIMIT ?
	`
	rows, err := db.conn.Query(query, tagName, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get sessions by tag: %w", err)
	}
	defer rows.Close()

	var sessions []SessionSummary
	for rows.Next() {
		var s SessionSummary
		var title sql.NullString
		if err := rows.Scan(&s.ID, &s.ProjectPath, &title, &s.UpdatedAt, &s.MessageCount); err != nil {
			return nil, err
		}
		if title.Valid {
			s.Title = title.String
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (db *DB) GetRelevantContext(projectPath string, query string, limit int) ([]Message, error) {
	searchResults, err := db.SearchMessages(query, limit*2)
	if err != nil {
		return nil, err
	}

	sessionIDs := make(map[string]bool)
	for _, r := range searchResults {
		sessionIDs[r.SessionID] = true
	}

	var messages []Message
	for sessionID := range sessionIDs {
		session, err := db.GetSession(sessionID)
		if err != nil {
			continue
		}
		if session.ProjectPath != projectPath {
			continue
		}
		sessionMsgs, err := db.GetMessages(sessionID)
		if err != nil {
			continue
		}
		for _, m := range sessionMsgs {
			if len(messages) >= limit {
				break
			}
			messages = append(messages, m)
		}
	}
	return messages, nil
}

func (db *DB) DeleteSession(id string) error {
	_, err := db.conn.Exec("DELETE FROM sessions WHERE id = ?", id)
	return err
}

func (db *DB) DeleteOldSessions(olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)
	result, err := db.conn.Exec("DELETE FROM sessions WHERE updated_at < ?", cutoff)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
