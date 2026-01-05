package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type WatchConfig struct {
	Patterns         []string
	BuildCommand     string
	TestCommand      string
	OnErrorCallback  func(ErrorEvent)
	OnRepairCallback func(RepairResult)
}

type ErrorEvent struct {
	Type       string
	File       string
	Line       int
	Message    string
	FullOutput string
	DetectedAt time.Time
	Language   string
}

type RepairResult struct {
	Error    ErrorEvent
	Success  bool
	Attempts int
	Solution string
	Command  string
	Output   string
	Duration time.Duration
}

type Watcher struct {
	config        WatchConfig
	ctx           context.Context
	cancel        context.CancelFunc
	mu            sync.Mutex
	running       bool
	lastBuild     time.Time
	errorHistory  []ErrorEvent
	repairHistory []RepairResult
}

var (
	activeWatcher *Watcher
	watcherMu     sync.Mutex
)

func init() {
	AvailableTools = append(AvailableTools,
		Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        "start_watch",
				Description: "Start watching for errors in the current project. Automatically detects build/test failures and attempts to repair them.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"build_command": {"type": "string", "description": "Build command to run (auto-detected if not provided)"},
						"test_command": {"type": "string", "description": "Test command to run"},
						"patterns": {"type": "array", "items": {"type": "string"}, "description": "File patterns to watch (e.g., *.go, *.py)"}
					},
					"additionalProperties": false
				}`),
			},
		},
		Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        "stop_watch",
				Description: "Stop the file watcher and auto-repair system.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {},
					"additionalProperties": false
				}`),
			},
		},
		Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        "watch_status",
				Description: "Get the current status of the file watcher, including recent errors and repairs.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {},
					"additionalProperties": false
				}`),
			},
		},
		Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        "trigger_build",
				Description: "Manually trigger a build/test cycle and auto-repair if errors found.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"command": {"type": "string", "description": "Command to run (uses detected build command if not provided)"}
					},
					"additionalProperties": false
				}`),
			},
		},
		Tool{
			Type: "function",
			Function: ToolFunction{
				Name:        "diagnose_error",
				Description: "Analyze an error and suggest or attempt repairs.",
				Parameters: json.RawMessage(`{
					"type": "object",
					"properties": {
						"error_text": {"type": "string", "description": "The error message to diagnose"},
						"auto_repair": {"type": "boolean", "description": "Attempt automatic repair if solution is found"}
					},
					"required": ["error_text"],
					"additionalProperties": false
				}`),
			},
		},
	)
}

func startWatch(args map[string]interface{}) (string, error) {
	watcherMu.Lock()
	defer watcherMu.Unlock()

	if activeWatcher != nil && activeWatcher.running {
		return "Watcher already running. Use stop_watch first.", nil
	}

	config := WatchConfig{}

	if cmd, ok := args["build_command"].(string); ok && cmd != "" {
		config.BuildCommand = cmd
	} else {
		config.BuildCommand = detectBuildCommand()
	}

	if cmd, ok := args["test_command"].(string); ok && cmd != "" {
		config.TestCommand = cmd
	} else {
		config.TestCommand = detectTestCommand()
	}

	if patterns, ok := args["patterns"].([]interface{}); ok {
		for _, p := range patterns {
			if s, ok := p.(string); ok {
				config.Patterns = append(config.Patterns, s)
			}
		}
	}

	if len(config.Patterns) == 0 {
		config.Patterns = detectWatchPatterns()
	}

	ctx, cancel := context.WithCancel(context.Background())
	watcher := &Watcher{
		config: config,
		ctx:    ctx,
		cancel: cancel,
	}

	activeWatcher = watcher
	go watcher.run()

	var result strings.Builder
	result.WriteString("Watch mode started\n")
	result.WriteString(fmt.Sprintf("Build command: %s\n", config.BuildCommand))
	if config.TestCommand != "" {
		result.WriteString(fmt.Sprintf("Test command: %s\n", config.TestCommand))
	}
	result.WriteString(fmt.Sprintf("Watching patterns: %v\n", config.Patterns))
	result.WriteString("\nErrors will be automatically detected and repairs attempted.")

	return result.String(), nil
}

func stopWatch(args map[string]interface{}) (string, error) {
	watcherMu.Lock()
	defer watcherMu.Unlock()

	if activeWatcher == nil || !activeWatcher.running {
		return "No watcher running.", nil
	}

	activeWatcher.cancel()
	activeWatcher.running = false

	repairs := len(activeWatcher.repairHistory)
	errors := len(activeWatcher.errorHistory)

	activeWatcher = nil

	return fmt.Sprintf("Watcher stopped. Detected %d errors, attempted %d repairs during session.", errors, repairs), nil
}

func watchStatus(args map[string]interface{}) (string, error) {
	watcherMu.Lock()
	defer watcherMu.Unlock()

	if activeWatcher == nil || !activeWatcher.running {
		return "No watcher running. Use start_watch to begin.", nil
	}

	var result strings.Builder
	result.WriteString("Watch Mode Status: ACTIVE\n")
	result.WriteString("========================\n\n")
	result.WriteString(fmt.Sprintf("Build command: %s\n", activeWatcher.config.BuildCommand))
	result.WriteString(fmt.Sprintf("Patterns: %v\n", activeWatcher.config.Patterns))
	result.WriteString(fmt.Sprintf("Last build: %s\n", activeWatcher.lastBuild.Format(time.RFC3339)))
	result.WriteString(fmt.Sprintf("Errors detected: %d\n", len(activeWatcher.errorHistory)))
	result.WriteString(fmt.Sprintf("Repairs attempted: %d\n", len(activeWatcher.repairHistory)))

	successCount := 0
	for _, r := range activeWatcher.repairHistory {
		if r.Success {
			successCount++
		}
	}
	if len(activeWatcher.repairHistory) > 0 {
		result.WriteString(fmt.Sprintf("Repair success rate: %.1f%%\n", float64(successCount)/float64(len(activeWatcher.repairHistory))*100))
	}

	if len(activeWatcher.errorHistory) > 0 {
		result.WriteString("\nRecent errors:\n")
		start := len(activeWatcher.errorHistory) - 5
		if start < 0 {
			start = 0
		}
		for _, e := range activeWatcher.errorHistory[start:] {
			result.WriteString(fmt.Sprintf("  [%s] %s:%d - %s\n", e.Type, e.File, e.Line, truncate(e.Message, 60)))
		}
	}

	return result.String(), nil
}

func triggerBuild(args map[string]interface{}) (string, error) {
	command, _ := args["command"].(string)
	if command == "" {
		command = detectBuildCommand()
	}

	output, err := runBuildCommand(command)
	if err != nil {
		errors := parseErrorOutput(output, detectLanguage())
		if len(errors) > 0 {
			var result strings.Builder
			result.WriteString(fmt.Sprintf("Build failed with %d error(s):\n\n", len(errors)))

			for i, e := range errors {
				result.WriteString(fmt.Sprintf("%d. [%s] %s:%d\n   %s\n\n", i+1, e.Type, e.File, e.Line, e.Message))

				repairResult := attemptRepair(e)
				if repairResult.Success {
					result.WriteString(fmt.Sprintf("   AUTO-REPAIRED: %s\n\n", repairResult.Solution))
				} else {
					result.WriteString("   Could not auto-repair. Manual intervention needed.\n\n")
				}
			}

			return result.String(), nil
		}
		return fmt.Sprintf("Build failed:\n%s", output), nil
	}

	return fmt.Sprintf("Build successful:\n%s", output), nil
}

func diagnoseError(args map[string]interface{}) (string, error) {
	errorText, _ := args["error_text"].(string)
	autoRepair, _ := args["auto_repair"].(bool)

	if errorText == "" {
		return "", fmt.Errorf("error_text is required")
	}

	errors := parseErrorOutput(errorText, detectLanguage())
	if len(errors) == 0 {
		errors = append(errors, ErrorEvent{
			Type:    "unknown",
			Message: errorText,
		})
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Diagnosed %d error(s):\n\n", len(errors)))

	for i, e := range errors {
		result.WriteString(fmt.Sprintf("%d. Type: %s\n", i+1, e.Type))
		if e.File != "" {
			result.WriteString(fmt.Sprintf("   File: %s:%d\n", e.File, e.Line))
		}
		result.WriteString(fmt.Sprintf("   Message: %s\n", e.Message))

		if knowledgeDB != nil {
			patterns, err := knowledgeDB.FindMatchingErrorPatterns(e.Message, getCurrentProjectPath(), 3)
			if err == nil && len(patterns) > 0 {
				result.WriteString("\n   Known solutions:\n")
				for _, p := range patterns {
					result.WriteString(fmt.Sprintf("   - %s (success rate: %d/%d)\n", p.Solution, p.SuccessCount, p.SuccessCount+p.FailureCount))
				}
			}
		}

		if autoRepair {
			repairResult := attemptRepair(e)
			if repairResult.Success {
				result.WriteString(fmt.Sprintf("\n   AUTO-REPAIRED: %s\n", repairResult.Solution))
			} else {
				result.WriteString("\n   Auto-repair failed or no solution found.\n")
			}
		}

		result.WriteString("\n")
	}

	return result.String(), nil
}

func (w *Watcher) run() {
	w.mu.Lock()
	w.running = true
	w.mu.Unlock()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	w.runBuildCycle()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			if w.hasFileChanges() {
				w.runBuildCycle()
			}
		}
	}
}

func (w *Watcher) hasFileChanges() bool {
	return true
}

func (w *Watcher) runBuildCycle() {
	w.mu.Lock()
	w.lastBuild = time.Now()
	w.mu.Unlock()

	output, err := runBuildCommand(w.config.BuildCommand)
	if err != nil {
		errors := parseErrorOutput(output, detectLanguage())
		for _, e := range errors {
			w.mu.Lock()
			w.errorHistory = append(w.errorHistory, e)
			w.mu.Unlock()

			if w.config.OnErrorCallback != nil {
				w.config.OnErrorCallback(e)
			}

			result := attemptRepair(e)
			w.mu.Lock()
			w.repairHistory = append(w.repairHistory, result)
			w.mu.Unlock()

			if w.config.OnRepairCallback != nil {
				w.config.OnRepairCallback(result)
			}
		}
	}

	if w.config.TestCommand != "" {
		output, err := runBuildCommand(w.config.TestCommand)
		if err != nil {
			errors := parseErrorOutput(output, detectLanguage())
			for _, e := range errors {
				e.Type = "test"
				w.mu.Lock()
				w.errorHistory = append(w.errorHistory, e)
				w.mu.Unlock()
			}
		}
	}
}

func runBuildCommand(command string) (string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "bash"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, shell, "-c", command)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func detectBuildCommand() string {
	cwd, _ := os.Getwd()

	if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
		return "go build ./..."
	}
	if _, err := os.Stat(filepath.Join(cwd, "Cargo.toml")); err == nil {
		return "cargo build"
	}
	if _, err := os.Stat(filepath.Join(cwd, "package.json")); err == nil {
		if _, err := os.Stat(filepath.Join(cwd, "node_modules", ".bin", "tsc")); err == nil {
			return "npx tsc --noEmit"
		}
		return "npm run build"
	}
	if _, err := os.Stat(filepath.Join(cwd, "requirements.txt")); err == nil {
		return "python -m py_compile *.py"
	}
	if _, err := os.Stat(filepath.Join(cwd, "Makefile")); err == nil {
		return "make"
	}

	return "echo 'No build command detected'"
}

func detectTestCommand() string {
	cwd, _ := os.Getwd()

	if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
		return "go test ./..."
	}
	if _, err := os.Stat(filepath.Join(cwd, "Cargo.toml")); err == nil {
		return "cargo test"
	}
	if _, err := os.Stat(filepath.Join(cwd, "package.json")); err == nil {
		return "npm test"
	}
	if _, err := os.Stat(filepath.Join(cwd, "pytest.ini")); err == nil {
		return "pytest"
	}

	return ""
}

func detectWatchPatterns() []string {
	cwd, _ := os.Getwd()

	if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
		return []string{"*.go"}
	}
	if _, err := os.Stat(filepath.Join(cwd, "Cargo.toml")); err == nil {
		return []string{"*.rs"}
	}
	if _, err := os.Stat(filepath.Join(cwd, "package.json")); err == nil {
		return []string{"*.js", "*.ts", "*.jsx", "*.tsx"}
	}
	if _, err := os.Stat(filepath.Join(cwd, "requirements.txt")); err == nil {
		return []string{"*.py"}
	}

	return []string{"*"}
}

func detectLanguage() string {
	cwd, _ := os.Getwd()

	if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
		return "go"
	}
	if _, err := os.Stat(filepath.Join(cwd, "Cargo.toml")); err == nil {
		return "rust"
	}
	if _, err := os.Stat(filepath.Join(cwd, "package.json")); err == nil {
		return "javascript"
	}
	if _, err := os.Stat(filepath.Join(cwd, "requirements.txt")); err == nil {
		return "python"
	}

	return "unknown"
}

func parseErrorOutput(output string, language string) []ErrorEvent {
	var errors []ErrorEvent

	switch language {
	case "go":
		errors = parseGoErrors(output)
	case "rust":
		errors = parseRustErrors(output)
	case "javascript", "typescript":
		errors = parseJSErrors(output)
	case "python":
		errors = parsePythonErrors(output)
	default:
		errors = parseGenericErrors(output)
	}

	return errors
}

func parseGoErrors(output string) []ErrorEvent {
	var errors []ErrorEvent

	goErrorRe := regexp.MustCompile(`^(.+\.go):(\d+):(\d+):\s*(.+)$`)
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()
		matches := goErrorRe.FindStringSubmatch(line)
		if len(matches) == 5 {
			lineNum := 0
			fmt.Sscanf(matches[2], "%d", &lineNum)
			errors = append(errors, ErrorEvent{
				Type:       "compile",
				File:       matches[1],
				Line:       lineNum,
				Message:    matches[4],
				Language:   "go",
				DetectedAt: time.Now(),
			})
		}
	}

	return errors
}

func parseRustErrors(output string) []ErrorEvent {
	var errors []ErrorEvent

	rustErrorRe := regexp.MustCompile(`error\[E\d+\]:\s*(.+)\n\s*-->\s*(.+):(\d+):(\d+)`)
	matches := rustErrorRe.FindAllStringSubmatch(output, -1)

	for _, m := range matches {
		if len(m) >= 4 {
			lineNum := 0
			fmt.Sscanf(m[3], "%d", &lineNum)
			errors = append(errors, ErrorEvent{
				Type:       "compile",
				File:       m[2],
				Line:       lineNum,
				Message:    m[1],
				Language:   "rust",
				DetectedAt: time.Now(),
			})
		}
	}

	return errors
}

func parseJSErrors(output string) []ErrorEvent {
	var errors []ErrorEvent

	tsErrorRe := regexp.MustCompile(`(.+\.tsx?)\((\d+),(\d+)\):\s*error\s+TS\d+:\s*(.+)`)
	matches := tsErrorRe.FindAllStringSubmatch(output, -1)

	for _, m := range matches {
		if len(m) >= 5 {
			lineNum := 0
			fmt.Sscanf(m[2], "%d", &lineNum)
			errors = append(errors, ErrorEvent{
				Type:       "compile",
				File:       m[1],
				Line:       lineNum,
				Message:    m[4],
				Language:   "typescript",
				DetectedAt: time.Now(),
			})
		}
	}

	return errors
}

func parsePythonErrors(output string) []ErrorEvent {
	var errors []ErrorEvent

	pyErrorRe := regexp.MustCompile(`File "(.+)", line (\d+)`)
	syntaxRe := regexp.MustCompile(`SyntaxError:\s*(.+)`)

	scanner := bufio.NewScanner(strings.NewReader(output))
	var lastFile string
	var lastLine int

	for scanner.Scan() {
		line := scanner.Text()

		fileMatch := pyErrorRe.FindStringSubmatch(line)
		if len(fileMatch) >= 3 {
			lastFile = fileMatch[1]
			fmt.Sscanf(fileMatch[2], "%d", &lastLine)
		}

		syntaxMatch := syntaxRe.FindStringSubmatch(line)
		if len(syntaxMatch) >= 2 && lastFile != "" {
			errors = append(errors, ErrorEvent{
				Type:       "syntax",
				File:       lastFile,
				Line:       lastLine,
				Message:    syntaxMatch[1],
				Language:   "python",
				DetectedAt: time.Now(),
			})
		}
	}

	return errors
}

func parseGenericErrors(output string) []ErrorEvent {
	var errors []ErrorEvent

	if strings.Contains(strings.ToLower(output), "error") {
		errors = append(errors, ErrorEvent{
			Type:       "unknown",
			Message:    truncate(output, 500),
			FullOutput: output,
			DetectedAt: time.Now(),
		})
	}

	return errors
}

func attemptRepair(e ErrorEvent) RepairResult {
	start := time.Now()
	result := RepairResult{
		Error:    e,
		Attempts: 0,
	}

	if knowledgeDB != nil {
		patterns, err := knowledgeDB.FindMatchingErrorPatterns(e.Message, getCurrentProjectPath(), 1)
		if err == nil && len(patterns) > 0 {
			pattern := patterns[0]
			if pattern.SolutionCommand != "" {
				result.Attempts++
				output, err := runBuildCommand(pattern.SolutionCommand)
				result.Output = output

				if err == nil {
					result.Success = true
					result.Solution = pattern.Solution
					result.Command = pattern.SolutionCommand
					result.Duration = time.Since(start)
					knowledgeDB.RecordErrorPatternResult(pattern.ID, true)
					return result
				}
				knowledgeDB.RecordErrorPatternResult(pattern.ID, false)
			}
		}
	}

	if e.File != "" && e.Line > 0 {
		result.Attempts++
		repaired := tryCommonFixes(e)
		if repaired {
			result.Success = true
			result.Solution = "Applied common fix pattern"
			result.Duration = time.Since(start)
			return result
		}
	}

	result.Duration = time.Since(start)
	return result
}

func tryCommonFixes(e ErrorEvent) bool {
	switch e.Language {
	case "go":
		if strings.Contains(e.Message, "undefined:") {
			return false
		}
		if strings.Contains(e.Message, "imported and not used") {
			return removeUnusedImport(e.File, e.Message)
		}
		if strings.Contains(e.Message, "declared but not used") {
			return false
		}
	case "javascript", "typescript":
		if strings.Contains(e.Message, "Cannot find module") {
			moduleName := extractModuleName(e.Message)
			if moduleName != "" {
				_, err := runBuildCommand(fmt.Sprintf("npm install %s", moduleName))
				return err == nil
			}
		}
	case "python":
		if strings.Contains(e.Message, "ModuleNotFoundError") {
			moduleName := extractPythonModule(e.Message)
			if moduleName != "" {
				_, err := runBuildCommand(fmt.Sprintf("pip install %s", moduleName))
				return err == nil
			}
		}
	}

	return false
}

func removeUnusedImport(file, message string) bool {
	importRe := regexp.MustCompile(`"(.+)" imported and not used`)
	matches := importRe.FindStringSubmatch(message)
	if len(matches) < 2 {
		return false
	}

	return false
}

func extractModuleName(message string) string {
	re := regexp.MustCompile(`Cannot find module '([^']+)'`)
	matches := re.FindStringSubmatch(message)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}

func extractPythonModule(message string) string {
	re := regexp.MustCompile(`No module named '([^']+)'`)
	matches := re.FindStringSubmatch(message)
	if len(matches) >= 2 {
		return matches[1]
	}
	return ""
}
