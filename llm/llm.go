package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"q/db"
	"q/tools"
	. "q/types"
	"q/util"
	"strings"
	"time"

	"github.com/hashicorp/go-retryablehttp"
)

type LLMClient struct {
	config           ModelConfig
	messages         []Message
	initialPromptLen int
	StreamCallback   func(string, error)
	ToolCallback     func(string, string)
	httpClient       *http.Client
	db               *db.DB
	sessionID        string
	projectPath      string
}

func NewLLMClient(cfg ModelConfig) *LLMClient {
	msgs := append([]Message(nil), cfg.Prompt...)
	if len(msgs) > 0 && msgs[0].Role == "system" {
		osInfo := util.GetOSInfo()
		shellEnv := os.Getenv("SHELL")
		if shellEnv == "" {
			shellEnv = os.Getenv("COMSPEC")
		}
		shellName := filepath.Base(shellEnv)
		cwd, _ := os.Getwd()
		envMsg := fmt.Sprintf("\n\nEnvironment: %s\nShell: %s\nWorking Directory: %s", osInfo, shellName, cwd)
		msgs[0].Content += envMsg
	}

	retryClient := retryablehttp.NewClient()
	retryClient.RetryMax = 3
	retryClient.RetryWaitMin = 1 * time.Second
	retryClient.RetryWaitMax = 10 * time.Second
	retryClient.Logger = nil

	client := &LLMClient{
		config:     cfg,
		messages:   msgs,
		httpClient: retryClient.StandardClient(),
	}
	client.httpClient.Timeout = time.Second * 300
	client.initialPromptLen = len(msgs)
	client.projectPath, _ = os.Getwd()

	database, err := db.Open()
	if err == nil {
		client.db = database
		session, err := database.CreateSession(client.projectPath)
		if err == nil {
			client.sessionID = session.ID
		}
		client.loadContextualMemory()
	}

	return client
}

func (c *LLMClient) loadContextualMemory() {
	if c.db == nil {
		return
	}

	sessions, err := c.db.GetRecentSessions(c.projectPath, 5)
	if err != nil || len(sessions) == 0 {
		return
	}

	var contextBuilder strings.Builder
	contextBuilder.WriteString("\n\n[Previous conversations in this directory:]\n")

	messagesAdded := 0
	maxMessages := 10
	for _, sess := range sessions {
		if sess.ID == c.sessionID {
			continue
		}
		msgs, err := c.db.GetMessages(sess.ID)
		if err != nil {
			continue
		}
		for _, m := range msgs {
			if messagesAdded >= maxMessages {
				break
			}
			if m.Role == "user" || m.Role == "assistant" {
				contextBuilder.WriteString(fmt.Sprintf("- %s: %s\n", m.Role, truncate(m.Content, 200)))
				messagesAdded++
			}
		}
	}

	if messagesAdded > 0 && len(c.messages) > 0 {
		c.messages[0].Content += contextBuilder.String()
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func (c *LLMClient) saveMessage(role, content string) {
	if c.db == nil || c.sessionID == "" {
		return
	}
	tokenCount := len(content) / 4
	c.db.AddMessage(c.sessionID, role, content, tokenCount)
}

func (c *LLMClient) Close() {
	if c.db != nil {
		c.db.Close()
	}
}

func (c *LLMClient) GetModelName() string {
	return c.config.Name
}

func (c *LLMClient) isOllamaCloud() bool {
	return c.config.Provider == "ollama-cloud" || strings.Contains(c.config.Endpoint, "ollama.com/api")
}

func (c *LLMClient) isOllamaLocal() bool {
	return strings.Contains(c.config.Endpoint, "11434")
}

func (c *LLMClient) supportsTools() bool {
	return !c.isOllamaLocal() && !c.isOllamaCloud()
}

type ToolCallPayload struct {
	Model       string        `json:"model"`
	Messages    []interface{} `json:"messages"`
	Tools       []tools.Tool  `json:"tools,omitempty"`
	ToolChoice  string        `json:"tool_choice,omitempty"`
	Temperature float32       `json:"temperature,omitempty"`
	Stream      bool          `json:"stream"`
}

type ToolCallResponse struct {
	ID      string `json:"id"`
	Choices []struct {
		Index   int `json:"index"`
		Message struct {
			Role      string           `json:"role"`
			Content   string           `json:"content"`
			ToolCalls []tools.ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		Delta struct {
			Content   string           `json:"content"`
			ToolCalls []tools.ToolCall `json:"tool_calls,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

type OllamaPayload struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type OllamaResponse struct {
	Model     string  `json:"model"`
	CreatedAt string  `json:"created_at"`
	Message   Message `json:"message"`
	Done      bool    `json:"done"`
}

func (c *LLMClient) createRequest(payload interface{}) (*http.Request, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}
	req, err := http.NewRequest("POST", c.config.Endpoint, bytes.NewBuffer(payloadBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if c.config.Auth != "" {
		if c.config.AuthHeader != "" {
			headerName := c.config.AuthHeader
			var headerValue string
			if strings.ToLower(headerName) == "authorization" {
				headerValue = "Bearer " + c.config.Auth
			} else {
				headerValue = c.config.Auth
			}
			req.Header.Set(headerName, headerValue)
		} else if strings.Contains(c.config.Endpoint, "openai.azure.com") {
			req.Header.Set("Api-Key", c.config.Auth)
		} else {
			req.Header.Set("Authorization", "Bearer "+c.config.Auth)
		}
	}

	if c.config.OrgID != "" {
		req.Header.Set("OpenAI-Organization", c.config.OrgID)
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (c *LLMClient) Query(query string) (string, error) {
	c.messages = append(c.messages, Message{Role: "user", Content: query})

	var finalContent string
	var err error

	if c.supportsTools() {
		finalContent, err = c.queryWithTools()
	} else if c.isOllamaCloud() || c.isOllamaLocal() {
		finalContent, err = c.queryOllama()
	} else {
		finalContent, err = c.queryOpenAI()
	}

	if err != nil {
		return "", err
	}

	c.messages = append(c.messages, Message{Role: "assistant", Content: finalContent})
	c.saveMessage("user", query)
	c.saveMessage("assistant", finalContent)
	return finalContent, nil
}

func (c *LLMClient) queryWithTools() (string, error) {
	maxIterations := 10
	var toolMessages []interface{}

	for i := 0; i < maxIterations; i++ {
		var msgInterfaces []interface{}
		for _, m := range c.messages {
			msgInterfaces = append(msgInterfaces, map[string]string{
				"role":    m.Role,
				"content": m.Content,
			})
		}
		msgInterfaces = append(msgInterfaces, toolMessages...)

		payload := ToolCallPayload{
			Model:       c.config.ModelName,
			Messages:    msgInterfaces,
			Tools:       tools.AvailableTools,
			ToolChoice:  "auto",
			Temperature: 0,
			Stream:      false,
		}

		req, err := c.createRequest(payload)
		if err != nil {
			return "", err
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("failed to make API request: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			return "", fmt.Errorf("API request failed (%s): %s", resp.Status, string(body))
		}

		var toolResp ToolCallResponse
		if err := json.Unmarshal(body, &toolResp); err != nil {
			return "", fmt.Errorf("failed to parse response: %w", err)
		}

		if len(toolResp.Choices) == 0 {
			return "", fmt.Errorf("no choices in response")
		}

		choice := toolResp.Choices[0]

		if len(choice.Message.ToolCalls) == 0 {
			content := choice.Message.Content
			if c.StreamCallback != nil {
				c.StreamCallback(content, nil)
			}
			return content, nil
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
			if c.ToolCallback != nil {
				c.ToolCallback(tc.Function.Name, tc.Function.Arguments)
			}

			result, execErr := tools.ExecuteTool(tc.Function.Name, tc.Function.Arguments)
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

	return "", fmt.Errorf("max tool iterations reached")
}

func (c *LLMClient) queryOpenAI() (string, error) {
	payload := Payload{
		Model:       c.config.ModelName,
		Messages:    c.messages,
		Temperature: 0,
		Stream:      true,
	}

	req, err := c.createRequest(payload)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API request failed (%s): %s", resp.Status, string(body))
	}

	return c.processOpenAIStream(resp)
}

func (c *LLMClient) processOpenAIStream(resp *http.Response) (string, error) {
	streamReader := bufio.NewReader(resp.Body)
	totalData := ""
	for {
		line, err := streamReader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if line == "data: [DONE]" {
			break
		}
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" {
				continue
			}

			var responseData ResponseData
			if err := json.Unmarshal([]byte(payload), &responseData); err != nil {
				continue
			}
			if len(responseData.Choices) == 0 {
				continue
			}
			content := responseData.Choices[0].Delta.Content
			totalData += content
			if c.StreamCallback != nil {
				c.StreamCallback(totalData, nil)
			}
		}
	}
	return totalData, nil
}

func (c *LLMClient) queryOllama() (string, error) {
	payload := OllamaPayload{
		Model:    c.config.ModelName,
		Messages: c.messages,
		Stream:   true,
	}

	req, err := c.createRequest(payload)
	if err != nil {
		return "", err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to make API request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("API request failed (%s): %s", resp.Status, string(body))
	}

	return c.processOllamaStream(resp)
}

func (c *LLMClient) processOllamaStream(resp *http.Response) (string, error) {
	streamReader := bufio.NewReader(resp.Body)
	totalData := ""
	for {
		line, err := streamReader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var ollamaResp OllamaResponse
		if err := json.Unmarshal([]byte(line), &ollamaResp); err != nil {
			continue
		}

		totalData += ollamaResp.Message.Content
		if c.StreamCallback != nil {
			c.StreamCallback(totalData, nil)
		}

		if ollamaResp.Done {
			break
		}
	}
	return totalData, nil
}

func (c *LLMClient) ClearMemory() error {
	c.messages = c.messages[:c.initialPromptLen]
	if c.db != nil && c.sessionID != "" {
		return c.db.DeleteSession(c.sessionID)
	}
	return nil
}
