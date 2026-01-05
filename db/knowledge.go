package db

import (
	"database/sql"
	"fmt"
	"time"
)

type KnowledgeEntity struct {
	ID              int64     `json:"id"`
	Type            string    `json:"type"`
	Name            string    `json:"name"`
	Value           string    `json:"value,omitempty"`
	ProjectPath     string    `json:"project_path,omitempty"`
	FirstSeen       time.Time `json:"first_seen"`
	LastSeen        time.Time `json:"last_seen"`
	OccurrenceCount int       `json:"occurrence_count"`
}

type KnowledgeRelation struct {
	ID         int64     `json:"id"`
	SourceID   int64     `json:"source_id"`
	Relation   string    `json:"relation"`
	TargetID   int64     `json:"target_id"`
	Confidence float64   `json:"confidence"`
	Context    string    `json:"context,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	LastUsed   time.Time `json:"last_used"`
	UseCount   int       `json:"use_count"`
}

type KnowledgeFact struct {
	ID                int64     `json:"id"`
	Category          string    `json:"category"`
	Subject           string    `json:"subject"`
	Predicate         string    `json:"predicate"`
	Object            string    `json:"object"`
	ProjectPath       string    `json:"project_path,omitempty"`
	Confidence        float64   `json:"confidence"`
	Source            string    `json:"source,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	LastVerified      time.Time `json:"last_verified"`
	VerificationCount int       `json:"verification_count"`
}

type ErrorPattern struct {
	ID              int64     `json:"id"`
	ErrorSignature  string    `json:"error_signature"`
	ErrorType       string    `json:"error_type"`
	Language        string    `json:"language,omitempty"`
	RootCause       string    `json:"root_cause,omitempty"`
	Solution        string    `json:"solution,omitempty"`
	SolutionCommand string    `json:"solution_command,omitempty"`
	SuccessCount    int       `json:"success_count"`
	FailureCount    int       `json:"failure_count"`
	ProjectPath     string    `json:"project_path,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	LastUsed        time.Time `json:"last_used"`
}

type RelatedKnowledge struct {
	Entity   KnowledgeEntity   `json:"entity"`
	Relation KnowledgeRelation `json:"relation"`
}

func (db *DB) UpsertEntity(entityType, name, value, projectPath string) (*KnowledgeEntity, error) {
	now := time.Now()

	var projectPathVal interface{}
	if projectPath != "" {
		projectPathVal = projectPath
	}

	_, err := db.conn.Exec(`
		INSERT INTO knowledge_entities (type, name, value, project_path, first_seen, last_seen, occurrence_count)
		VALUES (?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT(type, name, project_path) DO UPDATE SET
			value = COALESCE(excluded.value, knowledge_entities.value),
			last_seen = excluded.last_seen,
			occurrence_count = knowledge_entities.occurrence_count + 1
	`, entityType, name, value, projectPathVal, now, now)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert entity: %w", err)
	}

	return db.GetEntity(entityType, name, projectPath)
}

func (db *DB) GetEntity(entityType, name, projectPath string) (*KnowledgeEntity, error) {
	var projectPathVal interface{}
	if projectPath != "" {
		projectPathVal = projectPath
	}

	row := db.conn.QueryRow(`
		SELECT id, type, name, value, project_path, first_seen, last_seen, occurrence_count
		FROM knowledge_entities
		WHERE type = ? AND name = ? AND (project_path = ? OR (project_path IS NULL AND ? IS NULL))
	`, entityType, name, projectPathVal, projectPathVal)

	var e KnowledgeEntity
	var value, pp sql.NullString
	err := row.Scan(&e.ID, &e.Type, &e.Name, &value, &pp, &e.FirstSeen, &e.LastSeen, &e.OccurrenceCount)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get entity: %w", err)
	}

	if value.Valid {
		e.Value = value.String
	}
	if pp.Valid {
		e.ProjectPath = pp.String
	}

	return &e, nil
}

func (db *DB) GetEntityByID(id int64) (*KnowledgeEntity, error) {
	row := db.conn.QueryRow(`
		SELECT id, type, name, value, project_path, first_seen, last_seen, occurrence_count
		FROM knowledge_entities WHERE id = ?
	`, id)

	var e KnowledgeEntity
	var value, pp sql.NullString
	err := row.Scan(&e.ID, &e.Type, &e.Name, &value, &pp, &e.FirstSeen, &e.LastSeen, &e.OccurrenceCount)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get entity: %w", err)
	}

	if value.Valid {
		e.Value = value.String
	}
	if pp.Valid {
		e.ProjectPath = pp.String
	}

	return &e, nil
}

func (db *DB) SearchEntities(query string, entityType string, projectPath string, limit int) ([]KnowledgeEntity, error) {
	baseQuery := `
		SELECT e.id, e.type, e.name, e.value, e.project_path, e.first_seen, e.last_seen, e.occurrence_count
		FROM knowledge_fts f
		JOIN knowledge_entities e ON f.rowid = e.id
		WHERE knowledge_fts MATCH ?
	`
	args := []interface{}{query}

	if entityType != "" {
		baseQuery += " AND e.type = ?"
		args = append(args, entityType)
	}

	if projectPath != "" {
		baseQuery += " AND (e.project_path = ? OR e.project_path IS NULL)"
		args = append(args, projectPath)
	}

	baseQuery += " ORDER BY e.occurrence_count DESC, e.last_seen DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.conn.Query(baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to search entities: %w", err)
	}
	defer rows.Close()

	var entities []KnowledgeEntity
	for rows.Next() {
		var e KnowledgeEntity
		var value, pp sql.NullString
		if err := rows.Scan(&e.ID, &e.Type, &e.Name, &value, &pp, &e.FirstSeen, &e.LastSeen, &e.OccurrenceCount); err != nil {
			return nil, err
		}
		if value.Valid {
			e.Value = value.String
		}
		if pp.Valid {
			e.ProjectPath = pp.String
		}
		entities = append(entities, e)
	}

	return entities, nil
}

func (db *DB) UpsertRelation(sourceID int64, relation string, targetID int64, confidence float64, context string) (*KnowledgeRelation, error) {
	now := time.Now()

	_, err := db.conn.Exec(`
		INSERT INTO knowledge_relations (source_id, relation, target_id, confidence, context, created_at, last_used, use_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT(source_id, relation, target_id) DO UPDATE SET
			confidence = (knowledge_relations.confidence * knowledge_relations.use_count + excluded.confidence) / (knowledge_relations.use_count + 1),
			context = COALESCE(excluded.context, knowledge_relations.context),
			last_used = excluded.last_used,
			use_count = knowledge_relations.use_count + 1
	`, sourceID, relation, targetID, confidence, context, now, now)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert relation: %w", err)
	}

	return db.GetRelation(sourceID, relation, targetID)
}

func (db *DB) GetRelation(sourceID int64, relation string, targetID int64) (*KnowledgeRelation, error) {
	row := db.conn.QueryRow(`
		SELECT id, source_id, relation, target_id, confidence, context, created_at, last_used, use_count
		FROM knowledge_relations
		WHERE source_id = ? AND relation = ? AND target_id = ?
	`, sourceID, relation, targetID)

	var r KnowledgeRelation
	var ctx sql.NullString
	err := row.Scan(&r.ID, &r.SourceID, &r.Relation, &r.TargetID, &r.Confidence, &ctx, &r.CreatedAt, &r.LastUsed, &r.UseCount)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get relation: %w", err)
	}

	if ctx.Valid {
		r.Context = ctx.String
	}

	return &r, nil
}

func (db *DB) GetRelatedEntities(entityID int64, relation string, limit int) ([]RelatedKnowledge, error) {
	query := `
		SELECT r.id, r.source_id, r.relation, r.target_id, r.confidence, r.context, r.created_at, r.last_used, r.use_count,
		       e.id, e.type, e.name, e.value, e.project_path, e.first_seen, e.last_seen, e.occurrence_count
		FROM knowledge_relations r
		JOIN knowledge_entities e ON r.target_id = e.id
		WHERE r.source_id = ?
	`
	args := []interface{}{entityID}

	if relation != "" {
		query += " AND r.relation = ?"
		args = append(args, relation)
	}

	query += " ORDER BY r.confidence DESC, r.use_count DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get related entities: %w", err)
	}
	defer rows.Close()

	var results []RelatedKnowledge
	for rows.Next() {
		var rk RelatedKnowledge
		var ctx, value, pp sql.NullString
		if err := rows.Scan(
			&rk.Relation.ID, &rk.Relation.SourceID, &rk.Relation.Relation, &rk.Relation.TargetID,
			&rk.Relation.Confidence, &ctx, &rk.Relation.CreatedAt, &rk.Relation.LastUsed, &rk.Relation.UseCount,
			&rk.Entity.ID, &rk.Entity.Type, &rk.Entity.Name, &value, &pp,
			&rk.Entity.FirstSeen, &rk.Entity.LastSeen, &rk.Entity.OccurrenceCount,
		); err != nil {
			return nil, err
		}
		if ctx.Valid {
			rk.Relation.Context = ctx.String
		}
		if value.Valid {
			rk.Entity.Value = value.String
		}
		if pp.Valid {
			rk.Entity.ProjectPath = pp.String
		}
		results = append(results, rk)
	}

	return results, nil
}

func (db *DB) UpsertFact(category, subject, predicate, object, projectPath, source string, confidence float64) (*KnowledgeFact, error) {
	now := time.Now()

	var projectPathVal interface{}
	if projectPath != "" {
		projectPathVal = projectPath
	}

	_, err := db.conn.Exec(`
		INSERT INTO knowledge_facts (category, subject, predicate, object, project_path, confidence, source, created_at, last_verified, verification_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
		ON CONFLICT(category, subject, predicate, project_path) DO UPDATE SET
			object = excluded.object,
			confidence = (knowledge_facts.confidence * knowledge_facts.verification_count + excluded.confidence) / (knowledge_facts.verification_count + 1),
			source = COALESCE(excluded.source, knowledge_facts.source),
			last_verified = excluded.last_verified,
			verification_count = knowledge_facts.verification_count + 1
	`, category, subject, predicate, object, projectPathVal, confidence, source, now, now)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert fact: %w", err)
	}

	return db.GetFact(category, subject, predicate, projectPath)
}

func (db *DB) GetFact(category, subject, predicate, projectPath string) (*KnowledgeFact, error) {
	var projectPathVal interface{}
	if projectPath != "" {
		projectPathVal = projectPath
	}

	row := db.conn.QueryRow(`
		SELECT id, category, subject, predicate, object, project_path, confidence, source, created_at, last_verified, verification_count
		FROM knowledge_facts
		WHERE category = ? AND subject = ? AND predicate = ? AND (project_path = ? OR (project_path IS NULL AND ? IS NULL))
	`, category, subject, predicate, projectPathVal, projectPathVal)

	var f KnowledgeFact
	var pp, src sql.NullString
	err := row.Scan(&f.ID, &f.Category, &f.Subject, &f.Predicate, &f.Object, &pp, &f.Confidence, &src, &f.CreatedAt, &f.LastVerified, &f.VerificationCount)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get fact: %w", err)
	}

	if pp.Valid {
		f.ProjectPath = pp.String
	}
	if src.Valid {
		f.Source = src.String
	}

	return &f, nil
}

func (db *DB) GetFactsAbout(subject string, projectPath string, limit int) ([]KnowledgeFact, error) {
	query := `
		SELECT id, category, subject, predicate, object, project_path, confidence, source, created_at, last_verified, verification_count
		FROM knowledge_facts
		WHERE subject = ?
	`
	args := []interface{}{subject}

	if projectPath != "" {
		query += " AND (project_path = ? OR project_path IS NULL)"
		args = append(args, projectPath)
	}

	query += " ORDER BY confidence DESC, verification_count DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get facts: %w", err)
	}
	defer rows.Close()

	var facts []KnowledgeFact
	for rows.Next() {
		var f KnowledgeFact
		var pp, src sql.NullString
		if err := rows.Scan(&f.ID, &f.Category, &f.Subject, &f.Predicate, &f.Object, &pp, &f.Confidence, &src, &f.CreatedAt, &f.LastVerified, &f.VerificationCount); err != nil {
			return nil, err
		}
		if pp.Valid {
			f.ProjectPath = pp.String
		}
		if src.Valid {
			f.Source = src.String
		}
		facts = append(facts, f)
	}

	return facts, nil
}

func (db *DB) UpsertErrorPattern(signature, errorType, language, rootCause, solution, solutionCmd, projectPath string) (*ErrorPattern, error) {
	now := time.Now()

	var projectPathVal interface{}
	if projectPath != "" {
		projectPathVal = projectPath
	}

	_, err := db.conn.Exec(`
		INSERT INTO error_patterns (error_signature, error_type, language, root_cause, solution, solution_command, project_path, created_at, last_used)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(error_signature, project_path) DO UPDATE SET
			root_cause = COALESCE(excluded.root_cause, error_patterns.root_cause),
			solution = COALESCE(excluded.solution, error_patterns.solution),
			solution_command = COALESCE(excluded.solution_command, error_patterns.solution_command),
			last_used = excluded.last_used
	`, signature, errorType, language, rootCause, solution, solutionCmd, projectPathVal, now, now)
	if err != nil {
		return nil, fmt.Errorf("failed to upsert error pattern: %w", err)
	}

	return db.GetErrorPattern(signature, projectPath)
}

func (db *DB) GetErrorPattern(signature, projectPath string) (*ErrorPattern, error) {
	var projectPathVal interface{}
	if projectPath != "" {
		projectPathVal = projectPath
	}

	row := db.conn.QueryRow(`
		SELECT id, error_signature, error_type, language, root_cause, solution, solution_command, success_count, failure_count, project_path, created_at, last_used
		FROM error_patterns
		WHERE error_signature = ? AND (project_path = ? OR (project_path IS NULL AND ? IS NULL))
	`, signature, projectPathVal, projectPathVal)

	var ep ErrorPattern
	var lang, rootCause, solution, solutionCmd, pp sql.NullString
	err := row.Scan(&ep.ID, &ep.ErrorSignature, &ep.ErrorType, &lang, &rootCause, &solution, &solutionCmd, &ep.SuccessCount, &ep.FailureCount, &pp, &ep.CreatedAt, &ep.LastUsed)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get error pattern: %w", err)
	}

	if lang.Valid {
		ep.Language = lang.String
	}
	if rootCause.Valid {
		ep.RootCause = rootCause.String
	}
	if solution.Valid {
		ep.Solution = solution.String
	}
	if solutionCmd.Valid {
		ep.SolutionCommand = solutionCmd.String
	}
	if pp.Valid {
		ep.ProjectPath = pp.String
	}

	return &ep, nil
}

func (db *DB) FindMatchingErrorPatterns(errorText string, projectPath string, limit int) ([]ErrorPattern, error) {
	query := `
		SELECT id, error_signature, error_type, language, root_cause, solution, solution_command, success_count, failure_count, project_path, created_at, last_used
		FROM error_patterns
		WHERE ? LIKE '%' || error_signature || '%' OR error_signature LIKE '%' || ? || '%'
	`
	args := []interface{}{errorText, errorText}

	if projectPath != "" {
		query += " AND (project_path = ? OR project_path IS NULL)"
		args = append(args, projectPath)
	}

	query += " ORDER BY success_count DESC, last_used DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to find error patterns: %w", err)
	}
	defer rows.Close()

	var patterns []ErrorPattern
	for rows.Next() {
		var ep ErrorPattern
		var lang, rootCause, solution, solutionCmd, pp sql.NullString
		if err := rows.Scan(&ep.ID, &ep.ErrorSignature, &ep.ErrorType, &lang, &rootCause, &solution, &solutionCmd, &ep.SuccessCount, &ep.FailureCount, &pp, &ep.CreatedAt, &ep.LastUsed); err != nil {
			return nil, err
		}
		if lang.Valid {
			ep.Language = lang.String
		}
		if rootCause.Valid {
			ep.RootCause = rootCause.String
		}
		if solution.Valid {
			ep.Solution = solution.String
		}
		if solutionCmd.Valid {
			ep.SolutionCommand = solutionCmd.String
		}
		if pp.Valid {
			ep.ProjectPath = pp.String
		}
		patterns = append(patterns, ep)
	}

	return patterns, nil
}

func (db *DB) RecordErrorPatternResult(id int64, success bool) error {
	var field string
	if success {
		field = "success_count"
	} else {
		field = "failure_count"
	}

	_, err := db.conn.Exec(fmt.Sprintf(`
		UPDATE error_patterns SET %s = %s + 1, last_used = ? WHERE id = ?
	`, field, field), time.Now(), id)
	return err
}

func (db *DB) GetRecentEntities(projectPath string, entityType string, limit int) ([]KnowledgeEntity, error) {
	query := `
		SELECT id, type, name, value, project_path, first_seen, last_seen, occurrence_count
		FROM knowledge_entities
		WHERE 1=1
	`
	args := []interface{}{}

	if projectPath != "" {
		query += " AND (project_path = ? OR project_path IS NULL)"
		args = append(args, projectPath)
	}

	if entityType != "" {
		query += " AND type = ?"
		args = append(args, entityType)
	}

	query += " ORDER BY last_seen DESC LIMIT ?"
	args = append(args, limit)

	rows, err := db.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent entities: %w", err)
	}
	defer rows.Close()

	var entities []KnowledgeEntity
	for rows.Next() {
		var e KnowledgeEntity
		var value, pp sql.NullString
		if err := rows.Scan(&e.ID, &e.Type, &e.Name, &value, &pp, &e.FirstSeen, &e.LastSeen, &e.OccurrenceCount); err != nil {
			return nil, err
		}
		if value.Valid {
			e.Value = value.String
		}
		if pp.Valid {
			e.ProjectPath = pp.String
		}
		entities = append(entities, e)
	}

	return entities, nil
}

func (db *DB) GetKnowledgeSummary(projectPath string) (map[string]interface{}, error) {
	summary := make(map[string]interface{})

	var entityCount int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM knowledge_entities WHERE project_path = ? OR project_path IS NULL`, projectPath).Scan(&entityCount)
	if err != nil {
		return nil, err
	}
	summary["entity_count"] = entityCount

	var relationCount int
	err = db.conn.QueryRow(`SELECT COUNT(*) FROM knowledge_relations`).Scan(&relationCount)
	if err != nil {
		return nil, err
	}
	summary["relation_count"] = relationCount

	var factCount int
	err = db.conn.QueryRow(`SELECT COUNT(*) FROM knowledge_facts WHERE project_path = ? OR project_path IS NULL`, projectPath).Scan(&factCount)
	if err != nil {
		return nil, err
	}
	summary["fact_count"] = factCount

	var patternCount int
	err = db.conn.QueryRow(`SELECT COUNT(*) FROM error_patterns WHERE project_path = ? OR project_path IS NULL`, projectPath).Scan(&patternCount)
	if err != nil {
		return nil, err
	}
	summary["error_pattern_count"] = patternCount

	rows, err := db.conn.Query(`
		SELECT type, COUNT(*) as cnt FROM knowledge_entities 
		WHERE project_path = ? OR project_path IS NULL
		GROUP BY type ORDER BY cnt DESC
	`, projectPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entityTypes := make(map[string]int)
	for rows.Next() {
		var t string
		var c int
		rows.Scan(&t, &c)
		entityTypes[t] = c
	}
	summary["entity_types"] = entityTypes

	return summary, nil
}
