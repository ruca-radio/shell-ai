package db

import (
	"database/sql"
	"time"
)

// Session represents a conversation session in the database.
type Session struct {
	ID          string         `json:"id"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	ProjectPath string         `json:"project_path"`
	Title       sql.NullString `json:"title"`
	Summary     sql.NullString `json:"summary"`
}

// Message represents a single message within a session.
type Message struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"created_at"`
	TokenCount int       `json:"token_count"`
}

// ContextFile represents a file referenced during a session.
type ContextFile struct {
	ID          string    `json:"id"`
	SessionID   string    `json:"session_id"`
	FilePath    string    `json:"file_path"`
	ContentHash string    `json:"content_hash"`
	AddedAt     time.Time `json:"added_at"`
}

// Tag represents a tag that can be applied to sessions.
type Tag struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// SessionTag represents the many-to-many relationship between sessions and tags.
type SessionTag struct {
	SessionID string `json:"session_id"`
	TagID     int64  `json:"tag_id"`
}

// MessageRole constants for valid message roles.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
)

// SessionWithTags represents a session with its associated tags.
type SessionWithTags struct {
	Session
	Tags []Tag `json:"tags"`
}

// SessionWithMessages represents a session with all its messages.
type SessionWithMessages struct {
	Session
	Messages []Message `json:"messages"`
}

// FullSession represents a complete session with messages, tags, and context files.
type FullSession struct {
	Session
	Messages     []Message     `json:"messages"`
	Tags         []Tag         `json:"tags"`
	ContextFiles []ContextFile `json:"context_files"`
}

// SearchResult represents a full-text search result from messages_fts.
type SearchResult struct {
	MessageID string  `json:"message_id"`
	SessionID string  `json:"session_id"`
	Content   string  `json:"content"`
	Rank      float64 `json:"rank"`
}

// SessionSummary provides a lightweight view of a session for listing.
type SessionSummary struct {
	ID           string    `json:"id"`
	ProjectPath  string    `json:"project_path"`
	Title        string    `json:"title"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
}
