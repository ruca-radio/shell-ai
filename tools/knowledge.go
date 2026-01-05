package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"q/db"
	"strings"
)

var knowledgeDB *db.DB

func InitKnowledgeDB(database *db.DB) {
	knowledgeDB = database
}

func init() {
	AvailableTools = append(AvailableTools,
		Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        "learn_entity",
				Description: "Learn and remember an entity (file, command, error, solution, pattern, preference). The AI uses this to build knowledge about the environment over time.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"type": {"type": "string", "description": "Entity type: file, command, error, solution, pattern, preference"},
						"name": {"type": "string", "description": "Entity name/identifier"},
						"value": {"type": "string", "description": "Optional value or content"},
						"project_scoped": {"type": "boolean", "description": "If true, this knowledge is scoped to current project only"}
					},
					"required": ["type", "name"],
					"additionalProperties": false
				}`),
			},
		},
		Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        "learn_relation",
				Description: "Learn a relationship between two entities (e.g., error X was fixed_with solution Y).",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"source_type": {"type": "string", "description": "Source entity type"},
						"source_name": {"type": "string", "description": "Source entity name"},
						"relation": {"type": "string", "description": "Relationship: caused_by, fixed_with, similar_to, depends_on, prefers, often_uses"},
						"target_type": {"type": "string", "description": "Target entity type"},
						"target_name": {"type": "string", "description": "Target entity name"},
						"context": {"type": "string", "description": "Context description of when this was learned"}
					},
					"required": ["source_type", "source_name", "relation", "target_type", "target_name"],
					"additionalProperties": false
				}`),
			},
		},
		Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        "learn_fact",
				Description: "Learn a fact about the environment (e.g., 'user prefers vim', 'project uses postgres').",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"category": {"type": "string", "description": "Category: system, preference, pattern, solution"},
						"subject": {"type": "string", "description": "What this fact is about"},
						"predicate": {"type": "string", "description": "The relationship/property"},
						"object": {"type": "string", "description": "The value"},
						"project_scoped": {"type": "boolean", "description": "If true, scoped to current project"}
					},
					"required": ["category", "subject", "predicate", "object"],
					"additionalProperties": false
				}`),
			},
		},
		Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        "learn_error_pattern",
				Description: "Learn an error pattern and its solution for future auto-repair.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"error_signature": {"type": "string", "description": "Normalized error pattern (key identifying text)"},
						"error_type": {"type": "string", "description": "Type: compile, runtime, test, build, network, permission"},
						"language": {"type": "string", "description": "Programming language if applicable"},
						"root_cause": {"type": "string", "description": "What causes this error"},
						"solution": {"type": "string", "description": "How to fix it"},
						"solution_command": {"type": "string", "description": "Specific command that fixes it"}
					},
					"required": ["error_signature", "error_type"],
					"additionalProperties": false
				}`),
			},
		},
		Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        "recall_knowledge",
				Description: "Search the knowledge graph for relevant information about a topic.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"query": {"type": "string", "description": "Search query"},
						"entity_type": {"type": "string", "description": "Filter by entity type"},
						"limit": {"type": "integer", "description": "Max results (default 10)"}
					},
					"required": ["query"],
					"additionalProperties": false
				}`),
			},
		},
		Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        "recall_facts",
				Description: "Get all known facts about a subject.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"subject": {"type": "string", "description": "Subject to get facts about"},
						"limit": {"type": "integer", "description": "Max results (default 20)"}
					},
					"required": ["subject"],
					"additionalProperties": false
				}`),
			},
		},
		Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        "find_error_solution",
				Description: "Search for previously learned solutions to an error.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"error_text": {"type": "string", "description": "The error message or text"}
					},
					"required": ["error_text"],
					"additionalProperties": false
				}`),
			},
		},
		Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        "get_related",
				Description: "Get entities related to a given entity.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"entity_type": {"type": "string", "description": "Entity type"},
						"entity_name": {"type": "string", "description": "Entity name"},
						"relation": {"type": "string", "description": "Filter by relation type"},
						"limit": {"type": "integer", "description": "Max results (default 10)"}
					},
					"required": ["entity_type", "entity_name"],
					"additionalProperties": false
				}`),
			},
		},
		Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        "knowledge_summary",
				Description: "Get a summary of what the AI has learned about this project/environment.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {},
					"additionalProperties": false
				}`),
			},
		},
	)
}

func learnEntity(args map[string]interface{}) (string, error) {
	if knowledgeDB == nil {
		return "", fmt.Errorf("knowledge database not initialized")
	}

	entityType, _ := args["type"].(string)
	name, _ := args["name"].(string)
	value, _ := args["value"].(string)
	projectScoped, _ := args["project_scoped"].(bool)

	if entityType == "" || name == "" {
		return "", fmt.Errorf("type and name are required")
	}

	var projectPath string
	if projectScoped {
		projectPath = getCurrentProjectPath()
	}

	entity, err := knowledgeDB.UpsertEntity(entityType, name, value, projectPath)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Learned entity: [%s] %s (seen %d times)", entity.Type, entity.Name, entity.OccurrenceCount), nil
}

func learnRelation(args map[string]interface{}) (string, error) {
	if knowledgeDB == nil {
		return "", fmt.Errorf("knowledge database not initialized")
	}

	sourceType, _ := args["source_type"].(string)
	sourceName, _ := args["source_name"].(string)
	relation, _ := args["relation"].(string)
	targetType, _ := args["target_type"].(string)
	targetName, _ := args["target_name"].(string)
	context, _ := args["context"].(string)

	projectPath := getCurrentProjectPath()

	source, err := knowledgeDB.UpsertEntity(sourceType, sourceName, "", projectPath)
	if err != nil {
		return "", fmt.Errorf("failed to ensure source entity: %w", err)
	}

	target, err := knowledgeDB.UpsertEntity(targetType, targetName, "", projectPath)
	if err != nil {
		return "", fmt.Errorf("failed to ensure target entity: %w", err)
	}

	rel, err := knowledgeDB.UpsertRelation(source.ID, relation, target.ID, 1.0, context)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Learned relation: %s -[%s]-> %s (confidence: %.2f, seen %d times)",
		sourceName, rel.Relation, targetName, rel.Confidence, rel.UseCount), nil
}

func learnFact(args map[string]interface{}) (string, error) {
	if knowledgeDB == nil {
		return "", fmt.Errorf("knowledge database not initialized")
	}

	category, _ := args["category"].(string)
	subject, _ := args["subject"].(string)
	predicate, _ := args["predicate"].(string)
	object, _ := args["object"].(string)
	projectScoped, _ := args["project_scoped"].(bool)

	var projectPath string
	if projectScoped {
		projectPath = getCurrentProjectPath()
	}

	fact, err := knowledgeDB.UpsertFact(category, subject, predicate, object, projectPath, "ai_learned", 1.0)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Learned fact: %s %s %s (verified %d times)",
		fact.Subject, fact.Predicate, fact.Object, fact.VerificationCount), nil
}

func learnErrorPattern(args map[string]interface{}) (string, error) {
	if knowledgeDB == nil {
		return "", fmt.Errorf("knowledge database not initialized")
	}

	signature, _ := args["error_signature"].(string)
	errorType, _ := args["error_type"].(string)
	language, _ := args["language"].(string)
	rootCause, _ := args["root_cause"].(string)
	solution, _ := args["solution"].(string)
	solutionCmd, _ := args["solution_command"].(string)

	if signature == "" || errorType == "" {
		return "", fmt.Errorf("error_signature and error_type are required")
	}

	projectPath := getCurrentProjectPath()

	pattern, err := knowledgeDB.UpsertErrorPattern(signature, errorType, language, rootCause, solution, solutionCmd, projectPath)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Learned error pattern: [%s] %s\nRoot cause: %s\nSolution: %s",
		pattern.ErrorType, truncate(pattern.ErrorSignature, 50), pattern.RootCause, pattern.Solution), nil
}

func recallKnowledge(args map[string]interface{}) (string, error) {
	if knowledgeDB == nil {
		return "", fmt.Errorf("knowledge database not initialized")
	}

	query, _ := args["query"].(string)
	entityType, _ := args["entity_type"].(string)
	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	projectPath := getCurrentProjectPath()
	entities, err := knowledgeDB.SearchEntities(query, entityType, projectPath, limit)
	if err != nil {
		return "", err
	}

	if len(entities) == 0 {
		return "No relevant knowledge found.", nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d relevant entities:\n\n", len(entities)))

	for _, e := range entities {
		result.WriteString(fmt.Sprintf("[%s] %s", e.Type, e.Name))
		if e.Value != "" {
			result.WriteString(fmt.Sprintf(": %s", truncate(e.Value, 100)))
		}
		result.WriteString(fmt.Sprintf(" (seen %d times)\n", e.OccurrenceCount))
	}

	return result.String(), nil
}

func recallFacts(args map[string]interface{}) (string, error) {
	if knowledgeDB == nil {
		return "", fmt.Errorf("knowledge database not initialized")
	}

	subject, _ := args["subject"].(string)
	limit := 20
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	if subject == "" {
		return "", fmt.Errorf("subject is required")
	}

	projectPath := getCurrentProjectPath()
	facts, err := knowledgeDB.GetFactsAbout(subject, projectPath, limit)
	if err != nil {
		return "", err
	}

	if len(facts) == 0 {
		return fmt.Sprintf("No facts known about '%s'.", subject), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Facts about '%s':\n\n", subject))

	for _, f := range facts {
		scope := "global"
		if f.ProjectPath != "" {
			scope = "project"
		}
		result.WriteString(fmt.Sprintf("- %s %s %s [%s, confidence: %.2f]\n",
			f.Subject, f.Predicate, f.Object, scope, f.Confidence))
	}

	return result.String(), nil
}

func findErrorSolution(args map[string]interface{}) (string, error) {
	if knowledgeDB == nil {
		return "", fmt.Errorf("knowledge database not initialized")
	}

	errorText, _ := args["error_text"].(string)
	if errorText == "" {
		return "", fmt.Errorf("error_text is required")
	}

	projectPath := getCurrentProjectPath()
	patterns, err := knowledgeDB.FindMatchingErrorPatterns(errorText, projectPath, 5)
	if err != nil {
		return "", err
	}

	if len(patterns) == 0 {
		return "No matching error patterns found in knowledge base.", nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Found %d matching error patterns:\n\n", len(patterns)))

	for i, p := range patterns {
		result.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, p.ErrorType, truncate(p.ErrorSignature, 60)))
		if p.RootCause != "" {
			result.WriteString(fmt.Sprintf("   Root cause: %s\n", p.RootCause))
		}
		if p.Solution != "" {
			result.WriteString(fmt.Sprintf("   Solution: %s\n", p.Solution))
		}
		if p.SolutionCommand != "" {
			result.WriteString(fmt.Sprintf("   Command: %s\n", p.SolutionCommand))
		}
		result.WriteString(fmt.Sprintf("   Success rate: %d/%d\n\n", p.SuccessCount, p.SuccessCount+p.FailureCount))
	}

	return result.String(), nil
}

func getRelated(args map[string]interface{}) (string, error) {
	if knowledgeDB == nil {
		return "", fmt.Errorf("knowledge database not initialized")
	}

	entityType, _ := args["entity_type"].(string)
	entityName, _ := args["entity_name"].(string)
	relation, _ := args["relation"].(string)
	limit := 10
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	projectPath := getCurrentProjectPath()
	entity, err := knowledgeDB.GetEntity(entityType, entityName, projectPath)
	if err != nil {
		return "", err
	}
	if entity == nil {
		return fmt.Sprintf("Entity '%s' of type '%s' not found.", entityName, entityType), nil
	}

	related, err := knowledgeDB.GetRelatedEntities(entity.ID, relation, limit)
	if err != nil {
		return "", err
	}

	if len(related) == 0 {
		return fmt.Sprintf("No related entities found for '%s'.", entityName), nil
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Entities related to '%s':\n\n", entityName))

	for _, r := range related {
		result.WriteString(fmt.Sprintf("- %s -[%s]-> [%s] %s (confidence: %.2f)\n",
			entityName, r.Relation.Relation, r.Entity.Type, r.Entity.Name, r.Relation.Confidence))
	}

	return result.String(), nil
}

func knowledgeSummary(args map[string]interface{}) (string, error) {
	if knowledgeDB == nil {
		return "", fmt.Errorf("knowledge database not initialized")
	}

	projectPath := getCurrentProjectPath()
	summary, err := knowledgeDB.GetKnowledgeSummary(projectPath)
	if err != nil {
		return "", err
	}

	var result strings.Builder
	result.WriteString("Knowledge Graph Summary\n")
	result.WriteString("=======================\n\n")
	result.WriteString(fmt.Sprintf("Total entities: %d\n", summary["entity_count"]))
	result.WriteString(fmt.Sprintf("Total relations: %d\n", summary["relation_count"]))
	result.WriteString(fmt.Sprintf("Total facts: %d\n", summary["fact_count"]))
	result.WriteString(fmt.Sprintf("Error patterns: %d\n", summary["error_pattern_count"]))

	if entityTypes, ok := summary["entity_types"].(map[string]int); ok && len(entityTypes) > 0 {
		result.WriteString("\nEntity breakdown:\n")
		for t, c := range entityTypes {
			result.WriteString(fmt.Sprintf("  - %s: %d\n", t, c))
		}
	}

	return result.String(), nil
}

func getCurrentProjectPath() string {
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return ""
}
