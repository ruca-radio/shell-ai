package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type BackgroundTask struct {
	ID        string
	Command   string
	Status    string
	Output    string
	Error     string
	StartTime time.Time
	EndTime   time.Time
	Done      bool
}

var (
	backgroundTasks = make(map[string]*BackgroundTask)
	taskMutex       sync.RWMutex
	taskCounter     int
)

var AvailableTools = []Tool{
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "read_file",
			Description: "Read the contents of a file. Use when the user mentions a file or you need to see file contents.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Path to the file"}
				},
				"required": ["path"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "write_file",
			Description: "Write content to a file. Creates directories if needed.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Path to write to"},
					"content": {"type": "string", "description": "Content to write"}
				},
				"required": ["path", "content"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "run_command",
			Description: "Execute a shell command and return output. For quick commands that complete fast.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {"type": "string", "description": "Shell command to run"}
				},
				"required": ["command"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "run_background",
			Description: "Start a long-running command in background. Use for builds, servers, installs, or anything that takes time. Returns a task ID to check status later.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {"type": "string", "description": "Command to run in background"},
					"description": {"type": "string", "description": "Brief description of what this does"}
				},
				"required": ["command"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "check_task",
			Description: "Check status of a background task by ID.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"task_id": {"type": "string", "description": "Task ID to check"}
				},
				"required": ["task_id"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "list_tasks",
			Description: "List all background tasks and their status.",
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
			Name:        "kill_task",
			Description: "Kill a running background task.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"task_id": {"type": "string", "description": "Task ID to kill"}
				},
				"required": ["task_id"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "list_files",
			Description: "List files in a directory.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Directory path (defaults to current)"},
					"recursive": {"type": "boolean", "description": "List recursively"}
				},
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "search_files",
			Description: "Search for files by name pattern or content.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"pattern": {"type": "string", "description": "Filename pattern (glob)"},
					"content": {"type": "string", "description": "Text to search for in files"},
					"path": {"type": "string", "description": "Directory to search"}
				},
				"additionalProperties": false
			}`),
		},
	},
}

func ExecuteTool(name string, arguments string) (string, error) {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return "", fmt.Errorf("invalid arguments: %w", err)
	}

	switch name {
	case "read_file":
		return readFile(args)
	case "write_file":
		return writeFile(args)
	case "run_command":
		return runCommand(args)
	case "run_background":
		return runBackground(args)
	case "check_task":
		return checkTask(args)
	case "list_tasks":
		return listTasks()
	case "kill_task":
		return killTask(args)
	case "list_files":
		return listFiles(args)
	case "search_files":
		return searchFiles(args)
	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func readFile(args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path required")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("cannot access %s: %w", path, err)
	}

	if info.Size() > 1024*1024 {
		return "", fmt.Errorf("file too large (%d bytes), max 1MB", info.Size())
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func writeFile(args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path required")
	}
	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content required")
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return "", err
	}

	if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
		return "", err
	}

	return fmt.Sprintf("Wrote %d bytes to %s", len(content), absPath), nil
}

func runCommand(args map[string]interface{}) (string, error) {
	command, ok := args["command"].(string)
	if !ok {
		return "", fmt.Errorf("command required")
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "bash"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell, "-c", command)
	output, err := cmd.CombinedOutput()

	result := string(output)
	if ctx.Err() == context.DeadlineExceeded {
		result += "\n[Command timed out after 30s - use run_background for long commands]"
	} else if err != nil {
		result += fmt.Sprintf("\n[Exit: %v]", err)
	}

	return result, nil
}

func runBackground(args map[string]interface{}) (string, error) {
	command, ok := args["command"].(string)
	if !ok {
		return "", fmt.Errorf("command required")
	}

	desc := "Background task"
	if d, ok := args["description"].(string); ok {
		desc = d
	}

	taskMutex.Lock()
	taskCounter++
	taskID := fmt.Sprintf("task_%d", taskCounter)
	task := &BackgroundTask{
		ID:        taskID,
		Command:   command,
		Status:    "running",
		StartTime: time.Now(),
	}
	backgroundTasks[taskID] = task
	taskMutex.Unlock()

	go func() {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "bash"
		}

		cmd := exec.Command(shell, "-c", command)
		output, err := cmd.CombinedOutput()

		taskMutex.Lock()
		task.Output = string(output)
		task.EndTime = time.Now()
		task.Done = true
		if err != nil {
			task.Status = "failed"
			task.Error = err.Error()
		} else {
			task.Status = "completed"
		}
		taskMutex.Unlock()
	}()

	return fmt.Sprintf("Started background task %s: %s\nCommand: %s", taskID, desc, command), nil
}

func checkTask(args map[string]interface{}) (string, error) {
	taskID, ok := args["task_id"].(string)
	if !ok {
		return "", fmt.Errorf("task_id required")
	}

	taskMutex.RLock()
	task, exists := backgroundTasks[taskID]
	taskMutex.RUnlock()

	if !exists {
		return "", fmt.Errorf("task %s not found", taskID)
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Task: %s\n", task.ID))
	result.WriteString(fmt.Sprintf("Status: %s\n", task.Status))
	result.WriteString(fmt.Sprintf("Command: %s\n", task.Command))
	result.WriteString(fmt.Sprintf("Started: %s\n", task.StartTime.Format(time.RFC3339)))

	if task.Done {
		result.WriteString(fmt.Sprintf("Ended: %s\n", task.EndTime.Format(time.RFC3339)))
		result.WriteString(fmt.Sprintf("Duration: %s\n", task.EndTime.Sub(task.StartTime)))
		if task.Error != "" {
			result.WriteString(fmt.Sprintf("Error: %s\n", task.Error))
		}
		if task.Output != "" {
			result.WriteString(fmt.Sprintf("\nOutput:\n%s", task.Output))
		}
	} else {
		result.WriteString(fmt.Sprintf("Running for: %s\n", time.Since(task.StartTime)))
	}

	return result.String(), nil
}

func listTasks() (string, error) {
	taskMutex.RLock()
	defer taskMutex.RUnlock()

	if len(backgroundTasks) == 0 {
		return "No background tasks", nil
	}

	var result strings.Builder
	result.WriteString("Background Tasks:\n")
	for _, task := range backgroundTasks {
		status := task.Status
		if !task.Done {
			status = fmt.Sprintf("running (%s)", time.Since(task.StartTime).Truncate(time.Second))
		}
		result.WriteString(fmt.Sprintf("  %s: %s - %s\n", task.ID, status, truncate(task.Command, 50)))
	}

	return result.String(), nil
}

func killTask(args map[string]interface{}) (string, error) {
	taskID, ok := args["task_id"].(string)
	if !ok {
		return "", fmt.Errorf("task_id required")
	}

	taskMutex.Lock()
	task, exists := backgroundTasks[taskID]
	if exists && !task.Done {
		task.Status = "killed"
		task.Done = true
		task.EndTime = time.Now()
		task.Error = "Killed by user"
	}
	taskMutex.Unlock()

	if !exists {
		return "", fmt.Errorf("task %s not found", taskID)
	}

	return fmt.Sprintf("Task %s marked as killed", taskID), nil
}

func listFiles(args map[string]interface{}) (string, error) {
	path := "."
	if p, ok := args["path"].(string); ok && p != "" {
		path = p
	}

	recursive := false
	if r, ok := args["recursive"].(bool); ok {
		recursive = r
	}

	var files []string
	maxFiles := 100

	if recursive {
		count := 0
		filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
			if err != nil || count >= maxFiles {
				return nil
			}
			count++
			marker := ""
			if info.IsDir() {
				marker = "/"
			}
			files = append(files, p+marker)
			return nil
		})
	} else {
		entries, err := os.ReadDir(path)
		if err != nil {
			return "", err
		}
		for i, entry := range entries {
			if i >= maxFiles {
				files = append(files, fmt.Sprintf("... and %d more", len(entries)-maxFiles))
				break
			}
			name := entry.Name()
			if entry.IsDir() {
				name += "/"
			}
			files = append(files, name)
		}
	}

	return strings.Join(files, "\n"), nil
}

func searchFiles(args map[string]interface{}) (string, error) {
	path := "."
	if p, ok := args["path"].(string); ok && p != "" {
		path = p
	}

	pattern, _ := args["pattern"].(string)
	content, _ := args["content"].(string)

	var results []string
	maxResults := 50

	filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || len(results) >= maxResults {
			return nil
		}

		if pattern != "" {
			matched, _ := filepath.Match(pattern, filepath.Base(p))
			if !matched {
				return nil
			}
		}

		if content != "" {
			data, err := os.ReadFile(p)
			if err != nil || !strings.Contains(string(data), content) {
				return nil
			}
		}

		results = append(results, p)
		return nil
	})

	if len(results) == 0 {
		return "No matches found", nil
	}

	return strings.Join(results, "\n"), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
