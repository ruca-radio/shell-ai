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
	cancel    context.CancelFunc
	cmd       *exec.Cmd
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
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "append_file",
			Description: "Append content to the end of an existing file.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Path to the file"},
					"content": {"type": "string", "description": "Content to append"}
				},
				"required": ["path", "content"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "get_file_info",
			Description: "Get file metadata: size, permissions, modification time.",
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
			Name:        "git_status",
			Description: "Get git repository status: branch, changed files, staged changes. Only works in git repositories.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Repository path (defaults to current directory)"}
				},
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "git_diff",
			Description: "Show git diff of changed files. Can diff staged, unstaged, or specific files.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"staged": {"type": "boolean", "description": "Show staged changes only"},
					"file": {"type": "string", "description": "Specific file to diff"}
				},
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "git_log",
			Description: "Show recent git commit history.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"count": {"type": "integer", "description": "Number of commits to show (default 10)"},
					"oneline": {"type": "boolean", "description": "Compact one-line format"}
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
	case "append_file":
		return appendFile(args)
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
	case "get_file_info":
		return getFileInfo(args)
	case "git_status":
		return gitStatus(args)
	case "git_diff":
		return gitDiff(args)
	case "git_log":
		return gitLog(args)
	case "ssh_exec":
		return sshExec(args)
	case "ssh_upload":
		return sshUpload(args)
	case "ssh_download":
		return sshDownload(args)
	case "ping_host":
		return pingHost(args)
	case "port_scan":
		return portScan(args)
	case "lan_scan":
		return lanScan(args)
	case "wake_on_lan":
		return wakeOnLan(args)
	case "ssh_hosts":
		return sshHosts(args)
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

func appendFile(args map[string]interface{}) (string, error) {
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

	f, err := os.OpenFile(absPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return "", err
	}
	defer f.Close()

	n, err := f.WriteString(content)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Appended %d bytes to %s", n, absPath), nil
}

func getFileInfo(args map[string]interface{}) (string, error) {
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

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Path: %s\n", absPath))
	result.WriteString(fmt.Sprintf("Size: %d bytes\n", info.Size()))
	result.WriteString(fmt.Sprintf("Mode: %s\n", info.Mode()))
	result.WriteString(fmt.Sprintf("Modified: %s\n", info.ModTime().Format(time.RFC3339)))
	result.WriteString(fmt.Sprintf("IsDir: %t\n", info.IsDir()))

	return result.String(), nil
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

	ctx, cancel := context.WithCancel(context.Background())

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "bash"
	}

	cmd := exec.CommandContext(ctx, shell, "-c", command)

	taskMutex.Lock()
	taskCounter++
	taskID := fmt.Sprintf("task_%d", taskCounter)
	task := &BackgroundTask{
		ID:        taskID,
		Command:   command,
		Status:    "running",
		StartTime: time.Now(),
		cancel:    cancel,
		cmd:       cmd,
	}
	backgroundTasks[taskID] = task
	taskMutex.Unlock()

	go func() {
		output, err := cmd.CombinedOutput()

		taskMutex.Lock()
		task.Output = string(output)
		task.EndTime = time.Now()
		task.Done = true
		if ctx.Err() == context.Canceled {
			task.Status = "killed"
			task.Error = "Killed by user"
		} else if err != nil {
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
	if exists && !task.Done && task.cancel != nil {
		task.cancel()
	}
	taskMutex.Unlock()

	if !exists {
		return "", fmt.Errorf("task %s not found", taskID)
	}

	if task.Done {
		return fmt.Sprintf("Task %s already finished with status: %s", taskID, task.Status), nil
	}

	return fmt.Sprintf("Task %s killed", taskID), nil
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

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, ".svn": true, ".hg": true,
	"__pycache__": true, ".cache": true, "vendor": true, ".idea": true,
	".vscode": true, "dist": true, "build": true, ".next": true,
}

func isBinaryFile(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return true
	}
	defer f.Close()

	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil || n == 0 {
		return false
	}

	for _, b := range buf[:n] {
		if b == 0 {
			return true
		}
	}
	return false
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
		if err != nil {
			return nil
		}

		if info.IsDir() {
			name := info.Name()
			if skipDirs[name] || (len(name) > 0 && name[0] == '.') {
				return filepath.SkipDir
			}
			return nil
		}

		if len(results) >= maxResults {
			return filepath.SkipAll
		}

		if pattern != "" {
			matched, _ := filepath.Match(pattern, filepath.Base(p))
			if !matched {
				return nil
			}
		}

		if content != "" {
			if info.Size() > 1024*1024 || isBinaryFile(p) {
				return nil
			}
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

func gitStatus(args map[string]interface{}) (string, error) {
	path := "."
	if p, ok := args["path"].(string); ok && p != "" {
		path = p
	}

	cmd := exec.Command("git", "-C", path, "status", "--porcelain", "-b")
	output, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(string(output), "not a git repository") {
			return "Not a git repository", nil
		}
		return "", fmt.Errorf("git status failed: %s", string(output))
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) == 0 {
		return "Clean working directory", nil
	}

	var result strings.Builder
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			branch := strings.TrimPrefix(line, "## ")
			result.WriteString(fmt.Sprintf("Branch: %s\n", branch))
		} else if line != "" {
			status := line[:2]
			file := strings.TrimSpace(line[2:])
			switch {
			case status[0] == 'M' || status[1] == 'M':
				result.WriteString(fmt.Sprintf("  Modified: %s\n", file))
			case status[0] == 'A':
				result.WriteString(fmt.Sprintf("  Added: %s\n", file))
			case status[0] == 'D' || status[1] == 'D':
				result.WriteString(fmt.Sprintf("  Deleted: %s\n", file))
			case status == "??":
				result.WriteString(fmt.Sprintf("  Untracked: %s\n", file))
			case status[0] == 'R':
				result.WriteString(fmt.Sprintf("  Renamed: %s\n", file))
			default:
				result.WriteString(fmt.Sprintf("  %s: %s\n", status, file))
			}
		}
	}

	return result.String(), nil
}

func gitDiff(args map[string]interface{}) (string, error) {
	gitArgs := []string{"diff", "--stat"}

	if staged, ok := args["staged"].(bool); ok && staged {
		gitArgs = append(gitArgs, "--cached")
	}

	if file, ok := args["file"].(string); ok && file != "" {
		gitArgs = append(gitArgs, "--", file)
	}

	cmd := exec.Command("git", gitArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff failed: %s", string(output))
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		return "No changes", nil
	}

	return result, nil
}

func gitLog(args map[string]interface{}) (string, error) {
	count := 10
	if c, ok := args["count"].(float64); ok {
		count = int(c)
	}

	format := "%h %s (%cr) <%an>"
	if oneline, ok := args["oneline"].(bool); ok && oneline {
		format = "%h %s"
	}

	cmd := exec.Command("git", "log", fmt.Sprintf("-n%d", count), fmt.Sprintf("--format=%s", format))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git log failed: %s", string(output))
	}

	return strings.TrimSpace(string(output)), nil
}
