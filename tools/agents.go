package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

type AgentTask struct {
	ID         string
	Task       string
	Role       string
	Status     string
	Result     string
	Error      string
	StartTime  time.Time
	EndTime    time.Time
	Done       bool
	TokensUsed int
	cancel     context.CancelFunc
}

var (
	agentTasks   = make(map[string]*AgentTask)
	agentMutex   sync.RWMutex
	agentCounter int
)

var agentConfig struct {
	endpoint   string
	modelName  string
	apiKey     string
	authHeader string
}

func InitAgentConfig(endpoint, modelName, apiKey, authHeader string) {
	agentConfig.endpoint = endpoint
	agentConfig.modelName = modelName
	agentConfig.apiKey = apiKey
	agentConfig.authHeader = authHeader
}

var AgentTools = []Tool{
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "spawn_agent",
			Description: "Spawn a sub-agent to work on a specific task in background. The agent has access to all tools and will work autonomously. Use for complex subtasks, research, or parallel work.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"task": {"type": "string", "description": "Detailed task description for the agent"},
					"role": {"type": "string", "description": "Agent role/specialty (e.g., 'researcher', 'coder', 'reviewer')"}
				},
				"required": ["task"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "list_agents",
			Description: "List all spawned agents and their status.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {},
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "get_agent_result",
			Description: "Get the result from a completed agent task.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"agent_id": {"type": "string", "description": "Agent ID to get result from"}
				},
				"required": ["agent_id"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "wait_for_agent",
			Description: "Wait for an agent to complete and return its result. Use when you need the result before continuing.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"agent_id": {"type": "string", "description": "Agent ID to wait for"},
					"timeout_seconds": {"type": "integer", "description": "Max seconds to wait (default 120)"}
				},
				"required": ["agent_id"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "cancel_agent",
			Description: "Cancel a running agent task.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"agent_id": {"type": "string", "description": "Agent ID to cancel"}
				},
				"required": ["agent_id"],
				"additionalProperties": false
			}`),
		},
	},
}

func init() {
	AvailableTools = append(AvailableTools, AgentTools...)
}

type agentMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type agentPayload struct {
	Model       string        `json:"model"`
	Messages    []interface{} `json:"messages"`
	Tools       []Tool        `json:"tools,omitempty"`
	ToolChoice  string        `json:"tool_choice,omitempty"`
	Temperature float32       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream"`
}

type agentResponse struct {
	Choices []struct {
		Message struct {
			Role      string     `json:"role"`
			Content   string     `json:"content"`
			ToolCalls []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

func spawnAgent(args map[string]interface{}) (string, error) {
	task, _ := args["task"].(string)
	if task == "" {
		return "", fmt.Errorf("task required")
	}

	role := "assistant"
	if r, ok := args["role"].(string); ok && r != "" {
		role = r
	}

	if agentConfig.endpoint == "" || agentConfig.apiKey == "" {
		return "", fmt.Errorf("agent config not initialized - API endpoint and key required")
	}

	ctx, cancel := context.WithCancel(context.Background())

	agentMutex.Lock()
	agentCounter++
	agentID := fmt.Sprintf("agent_%d", agentCounter)
	agent := &AgentTask{
		ID:        agentID,
		Task:      task,
		Role:      role,
		Status:    "running",
		StartTime: time.Now(),
		cancel:    cancel,
	}
	agentTasks[agentID] = agent
	agentMutex.Unlock()

	go runAgent(ctx, agent)

	return fmt.Sprintf("Spawned %s (role: %s)\nTask: %s", agentID, role, truncateStr(task, 100)), nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func runAgent(ctx context.Context, agent *AgentTask) {
	defer func() {
		agentMutex.Lock()
		agent.EndTime = time.Now()
		agent.Done = true
		agentMutex.Unlock()
	}()

	systemPrompt := fmt.Sprintf(`You are a focused sub-agent with role: %s

Your task: %s

You have access to tools for file operations, commands, git, SSH, and network tasks.
Work autonomously to complete your task. Be thorough but efficient.
When done, provide a clear summary of what you accomplished or found.`, agent.Role, agent.Task)

	agentToolsForSubagent := filterAgentTools(AvailableTools)

	messages := []interface{}{
		map[string]string{"role": "system", "content": systemPrompt},
		map[string]string{"role": "user", "content": agent.Task},
	}

	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 3
	retryClient.RetryWaitMin = 1 * time.Second
	retryClient.RetryWaitMax = 10 * time.Second
	retryClient.Logger = nil
	httpClient := retryClient.StandardClient()
	httpClient.Timeout = 5 * time.Minute

	maxIterations := 15
	var toolMessages []interface{}
	var totalTokens int

	for i := 0; i < maxIterations; i++ {
		select {
		case <-ctx.Done():
			agentMutex.Lock()
			agent.Status = "cancelled"
			agent.Error = "Cancelled by user"
			agentMutex.Unlock()
			return
		default:
		}

		allMessages := append(messages, toolMessages...)

		payload := agentPayload{
			Model:       agentConfig.modelName,
			Messages:    allMessages,
			Tools:       agentToolsForSubagent,
			ToolChoice:  "auto",
			Temperature: 0,
			Stream:      false,
		}

		payloadBytes, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, "POST", agentConfig.endpoint, bytes.NewBuffer(payloadBytes))
		if err != nil {
			agentMutex.Lock()
			agent.Status = "failed"
			agent.Error = err.Error()
			agentMutex.Unlock()
			return
		}

		if agentConfig.authHeader != "" {
			if strings.ToLower(agentConfig.authHeader) == "authorization" {
				req.Header.Set(agentConfig.authHeader, "Bearer "+agentConfig.apiKey)
			} else {
				req.Header.Set(agentConfig.authHeader, agentConfig.apiKey)
			}
		} else {
			req.Header.Set("Authorization", "Bearer "+agentConfig.apiKey)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpClient.Do(req)
		if err != nil {
			agentMutex.Lock()
			agent.Status = "failed"
			agent.Error = err.Error()
			agentMutex.Unlock()
			return
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			agentMutex.Lock()
			agent.Status = "failed"
			agent.Error = fmt.Sprintf("API error %d: %s", resp.StatusCode, string(body))
			agentMutex.Unlock()
			return
		}

		var apiResp agentResponse
		if err := json.Unmarshal(body, &apiResp); err != nil {
			agentMutex.Lock()
			agent.Status = "failed"
			agent.Error = "Failed to parse API response"
			agentMutex.Unlock()
			return
		}

		totalTokens += apiResp.Usage.TotalTokens

		if len(apiResp.Choices) == 0 {
			agentMutex.Lock()
			agent.Status = "failed"
			agent.Error = "No response choices"
			agentMutex.Unlock()
			return
		}

		choice := apiResp.Choices[0]

		if len(choice.Message.ToolCalls) == 0 {
			agentMutex.Lock()
			agent.Status = "completed"
			agent.Result = choice.Message.Content
			agent.TokensUsed = totalTokens
			agentMutex.Unlock()
			return
		}

		assistantMsg := map[string]interface{}{
			"role":       "assistant",
			"tool_calls": choice.Message.ToolCalls,
		}
		if choice.Message.Content != "" {
			assistantMsg["content"] = choice.Message.Content
		}
		toolMessages = append(toolMessages, assistantMsg)

		for _, tc := range choice.Message.ToolCalls {
			if isAgentTool(tc.Function.Name) {
				toolMsg := map[string]interface{}{
					"role":         "tool",
					"tool_call_id": tc.ID,
					"content":      "Sub-agents cannot spawn other agents",
				}
				toolMessages = append(toolMessages, toolMsg)
				continue
			}

			result, execErr := ExecuteTool(tc.Function.Name, tc.Function.Arguments)
			if execErr != nil {
				result = fmt.Sprintf("Error: %v", execErr)
			}

			toolMsg := map[string]interface{}{
				"role":         "tool",
				"tool_call_id": tc.ID,
				"content":      result,
			}
			toolMessages = append(toolMessages, toolMsg)
		}
	}

	agentMutex.Lock()
	agent.Status = "completed"
	agent.Result = "Agent reached maximum iterations without final response"
	agent.TokensUsed = totalTokens
	agentMutex.Unlock()
}

func filterAgentTools(tools []Tool) []Tool {
	var filtered []Tool
	for _, t := range tools {
		if !isAgentTool(t.Function.Name) {
			filtered = append(filtered, t)
		}
	}
	return filtered
}

func isAgentTool(name string) bool {
	agentToolNames := map[string]bool{
		"spawn_agent":      true,
		"list_agents":      true,
		"get_agent_result": true,
		"wait_for_agent":   true,
		"cancel_agent":     true,
	}
	return agentToolNames[name]
}

func listAgents(args map[string]interface{}) (string, error) {
	agentMutex.RLock()
	defer agentMutex.RUnlock()

	if len(agentTasks) == 0 {
		return "No agents spawned", nil
	}

	var result strings.Builder
	result.WriteString("Agents:\n")
	for _, agent := range agentTasks {
		duration := time.Since(agent.StartTime).Truncate(time.Second)
		if agent.Done {
			duration = agent.EndTime.Sub(agent.StartTime).Truncate(time.Second)
		}
		result.WriteString(fmt.Sprintf("  %s [%s] (%s) - %s\n",
			agent.ID, agent.Status, duration, truncateStr(agent.Task, 50)))
		if agent.TokensUsed > 0 {
			result.WriteString(fmt.Sprintf("    Tokens: %d\n", agent.TokensUsed))
		}
	}

	return result.String(), nil
}

func getAgentResult(args map[string]interface{}) (string, error) {
	agentID, _ := args["agent_id"].(string)
	if agentID == "" {
		return "", fmt.Errorf("agent_id required")
	}

	agentMutex.RLock()
	agent, exists := agentTasks[agentID]
	agentMutex.RUnlock()

	if !exists {
		return "", fmt.Errorf("agent %s not found", agentID)
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Agent: %s\n", agent.ID))
	result.WriteString(fmt.Sprintf("Role: %s\n", agent.Role))
	result.WriteString(fmt.Sprintf("Status: %s\n", agent.Status))
	result.WriteString(fmt.Sprintf("Task: %s\n", agent.Task))

	if agent.Done {
		result.WriteString(fmt.Sprintf("Duration: %s\n", agent.EndTime.Sub(agent.StartTime).Truncate(time.Second)))
		result.WriteString(fmt.Sprintf("Tokens: %d\n", agent.TokensUsed))
		if agent.Error != "" {
			result.WriteString(fmt.Sprintf("Error: %s\n", agent.Error))
		}
		if agent.Result != "" {
			result.WriteString(fmt.Sprintf("\nResult:\n%s", agent.Result))
		}
	} else {
		result.WriteString(fmt.Sprintf("Running for: %s\n", time.Since(agent.StartTime).Truncate(time.Second)))
	}

	return result.String(), nil
}

func waitForAgent(args map[string]interface{}) (string, error) {
	agentID, _ := args["agent_id"].(string)
	if agentID == "" {
		return "", fmt.Errorf("agent_id required")
	}

	timeout := 120
	if t, ok := args["timeout_seconds"].(float64); ok {
		timeout = int(t)
	}

	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	pollInterval := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		agentMutex.RLock()
		agent, exists := agentTasks[agentID]
		if !exists {
			agentMutex.RUnlock()
			return "", fmt.Errorf("agent %s not found", agentID)
		}
		done := agent.Done
		agentMutex.RUnlock()

		if done {
			return getAgentResult(args)
		}

		time.Sleep(pollInterval)
	}

	return "", fmt.Errorf("timeout waiting for agent %s after %d seconds", agentID, timeout)
}

func cancelAgent(args map[string]interface{}) (string, error) {
	agentID, _ := args["agent_id"].(string)
	if agentID == "" {
		return "", fmt.Errorf("agent_id required")
	}

	agentMutex.Lock()
	agent, exists := agentTasks[agentID]
	if exists && !agent.Done && agent.cancel != nil {
		agent.cancel()
	}
	agentMutex.Unlock()

	if !exists {
		return "", fmt.Errorf("agent %s not found", agentID)
	}

	if agent.Done {
		return fmt.Sprintf("Agent %s already finished with status: %s", agentID, agent.Status), nil
	}

	return fmt.Sprintf("Agent %s cancelled", agentID), nil
}

func GetActiveAgentCount() int {
	agentMutex.RLock()
	defer agentMutex.RUnlock()

	count := 0
	for _, agent := range agentTasks {
		if !agent.Done {
			count++
		}
	}
	return count
}

func ClearCompletedAgents() int {
	agentMutex.Lock()
	defer agentMutex.Unlock()

	cleared := 0
	for id, agent := range agentTasks {
		if agent.Done {
			delete(agentTasks, id)
			cleared++
		}
	}
	return cleared
}
