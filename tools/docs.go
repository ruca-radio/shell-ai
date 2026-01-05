package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"q/db"
	"regexp"
	"strings"
	"time"
)

var docsDB *db.DB

func InitDocsDB(database *db.DB) {
	docsDB = database
}

var DocsTools = []Tool{
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "get_docs",
			Description: "Get documentation for a command, program, or topic. Fetches from man pages, --help, tldr, cheat.sh, or cache.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"name": {"type": "string", "description": "Command or topic name (e.g., 'git', 'docker', 'systemctl')"},
					"source": {"type": "string", "description": "Preferred source: 'man', 'help', 'tldr', 'cheat', 'info', 'auto' (default: auto)"}
				},
				"required": ["name"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "search_docs",
			Description: "Search cached documentation for a topic or keyword.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"query": {"type": "string", "description": "Search query"}
				},
				"required": ["query"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "list_docs",
			Description: "List all cached documentation entries.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"limit": {"type": "integer", "description": "Max entries to return (default 20)"}
				},
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "fetch_web_docs",
			Description: "Fetch documentation from a URL and cache it.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"url": {"type": "string", "description": "URL to fetch documentation from"},
					"name": {"type": "string", "description": "Name to store the doc under"}
				},
				"required": ["url", "name"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "get_system_info",
			Description: "Get system information: OS, kernel, installed packages, services.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"type": {"type": "string", "description": "Info type: 'os', 'packages', 'services', 'all' (default: os)"}
				},
				"additionalProperties": false
			}`),
		},
	},
}

func init() {
	AvailableTools = append(AvailableTools, DocsTools...)
}

func getDocs(args map[string]interface{}) (string, error) {
	name, _ := args["name"].(string)
	if name == "" {
		return "", fmt.Errorf("name required")
	}

	source := "auto"
	if s, ok := args["source"].(string); ok && s != "" {
		source = s
	}

	name = strings.TrimSpace(strings.ToLower(name))

	if docsDB != nil {
		cached, err := docsDB.GetDoc(name, source)
		if err == nil && cached != nil && time.Now().Before(cached.ExpiresAt) {
			return formatDocResult(cached), nil
		}
	}

	var content, docSource, summary string
	var err error

	switch source {
	case "man":
		content, err = fetchManPage(name)
		docSource = "man"
	case "help":
		content, err = fetchHelp(name)
		docSource = "help"
	case "tldr":
		content, err = fetchTLDR(name)
		docSource = "tldr"
	case "cheat":
		content, err = fetchCheatSh(name)
		docSource = "cheat.sh"
	case "info":
		content, err = fetchInfo(name)
		docSource = "info"
	default:
		content, docSource, err = fetchAuto(name)
	}

	if err != nil {
		return "", err
	}

	summary = generateSummary(content)

	if docsDB != nil {
		ttl := 7 * 24 * time.Hour
		docsDB.SaveDoc(name, docSource, content, summary, "", ttl)
	}

	return fmt.Sprintf("[Source: %s]\n\n%s", docSource, content), nil
}

func fetchAuto(name string) (string, string, error) {
	if content, err := fetchTLDR(name); err == nil && content != "" {
		return content, "tldr", nil
	}

	if content, err := fetchCheatSh(name); err == nil && content != "" {
		return content, "cheat.sh", nil
	}

	if content, err := fetchHelp(name); err == nil && content != "" {
		return content, "help", nil
	}

	if content, err := fetchManPage(name); err == nil && content != "" {
		return content, "man", nil
	}

	return "", "", fmt.Errorf("no documentation found for '%s'", name)
}

func fetchManPage(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "man", name)
	cmd.Env = append(os.Environ(), "MANPAGER=cat", "MANWIDTH=80", "COLUMNS=80")

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("man page not found for '%s'", name)
	}

	content := string(output)
	content = stripANSI(content)
	content = strings.TrimSpace(content)

	if len(content) > 50000 {
		content = content[:50000] + "\n\n[Truncated - full man page is longer]"
	}

	return content, nil
}

func fetchHelp(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, "--help")
	output, err := cmd.CombinedOutput()

	if err != nil {
		cmd = exec.CommandContext(ctx, name, "-h")
		output, err = cmd.CombinedOutput()
	}

	if len(output) == 0 {
		return "", fmt.Errorf("no help output for '%s'", name)
	}

	content := stripANSI(string(output))
	return strings.TrimSpace(content), nil
}

func fetchTLDR(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://raw.githubusercontent.com/tldr-pages/tldr/main/pages/common/%s.md", name)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		url = fmt.Sprintf("https://raw.githubusercontent.com/tldr-pages/tldr/main/pages/linux/%s.md", name)
		req, _ = http.NewRequestWithContext(ctx, "GET", url, nil)
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
	}

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("tldr page not found for '%s'", name)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func fetchCheatSh(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := fmt.Sprintf("https://cheat.sh/%s?T", name)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "curl")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("cheat.sh page not found for '%s'", name)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	content := stripANSI(string(body))
	return strings.TrimSpace(content), nil
}

func fetchInfo(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "info", "--subnodes", "-o", "-", name)
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("info page not found for '%s'", name)
	}

	content := string(output)
	if len(content) > 50000 {
		content = content[:50000] + "\n\n[Truncated]"
	}

	return strings.TrimSpace(content), nil
}

func searchDocs(args map[string]interface{}) (string, error) {
	query, _ := args["query"].(string)
	if query == "" {
		return "", fmt.Errorf("query required")
	}

	if docsDB == nil {
		return "Documentation database not initialized", nil
	}

	results, err := docsDB.SearchDocs(query, 10)
	if err != nil {
		return "", err
	}

	if len(results) == 0 {
		return fmt.Sprintf("No cached docs match '%s'. Use get_docs to fetch documentation first.", query), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d docs matching '%s':\n\n", len(results), query))
	for _, r := range results {
		sb.WriteString(fmt.Sprintf("  %s [%s]\n", r.Name, r.Source))
		if r.Summary != "" {
			sb.WriteString(fmt.Sprintf("    %s\n", r.Summary))
		}
	}

	return sb.String(), nil
}

func listDocs(args map[string]interface{}) (string, error) {
	limit := 20
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	if docsDB == nil {
		return "Documentation database not initialized", nil
	}

	docs, err := docsDB.ListDocs(limit)
	if err != nil {
		return "", err
	}

	if len(docs) == 0 {
		return "No cached documentation. Use get_docs to fetch documentation.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Cached documentation (%d entries):\n\n", len(docs)))
	for _, d := range docs {
		age := time.Since(d.FetchedAt).Truncate(time.Hour)
		sb.WriteString(fmt.Sprintf("  %s [%s] - fetched %s ago\n", d.Name, d.Source, age))
	}

	return sb.String(), nil
}

func fetchWebDocs(args map[string]interface{}) (string, error) {
	url, _ := args["url"].(string)
	name, _ := args["name"].(string)

	if url == "" || name == "" {
		return "", fmt.Errorf("url and name required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; shell-ai/1.0)")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("failed to fetch %s: HTTP %d", url, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 500000))
	if err != nil {
		return "", err
	}

	content := string(body)
	content = stripHTML(content)
	content = strings.TrimSpace(content)

	summary := generateSummary(content)

	if docsDB != nil {
		ttl := 24 * time.Hour
		docsDB.SaveDoc(name, "web:"+url, content, summary, "", ttl)
	}

	if len(content) > 5000 {
		return fmt.Sprintf("Fetched and cached documentation for '%s' from %s (%d bytes)\n\nPreview:\n%s...",
			name, url, len(content), content[:5000]), nil
	}

	return fmt.Sprintf("Fetched and cached documentation for '%s' from %s:\n\n%s", name, url, content), nil
}

func getSystemInfo(args map[string]interface{}) (string, error) {
	infoType := "os"
	if t, ok := args["type"].(string); ok && t != "" {
		infoType = t
	}

	var result strings.Builder

	switch infoType {
	case "os":
		result.WriteString(getOSInfo())
	case "packages":
		result.WriteString(getPackageInfo())
	case "services":
		result.WriteString(getServiceInfo())
	case "all":
		result.WriteString("=== OS Info ===\n")
		result.WriteString(getOSInfo())
		result.WriteString("\n=== Installed Packages ===\n")
		result.WriteString(getPackageInfo())
		result.WriteString("\n=== Services ===\n")
		result.WriteString(getServiceInfo())
	default:
		return "", fmt.Errorf("unknown info type: %s (use: os, packages, services, all)", infoType)
	}

	return result.String(), nil
}

func getOSInfo() string {
	var sb strings.Builder

	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		sb.WriteString("OS Release:\n")
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") ||
				strings.HasPrefix(line, "VERSION=") ||
				strings.HasPrefix(line, "ID=") {
				sb.WriteString("  " + line + "\n")
			}
		}
	}

	if out, err := exec.Command("uname", "-a").Output(); err == nil {
		sb.WriteString("\nKernel: " + strings.TrimSpace(string(out)) + "\n")
	}

	if out, err := exec.Command("hostname").Output(); err == nil {
		sb.WriteString("Hostname: " + strings.TrimSpace(string(out)) + "\n")
	}

	if out, err := exec.Command("uptime", "-p").Output(); err == nil {
		sb.WriteString("Uptime: " + strings.TrimSpace(string(out)) + "\n")
	}

	return sb.String()
}

func getPackageInfo() string {
	var sb strings.Builder

	if _, err := exec.LookPath("dpkg"); err == nil {
		if out, err := exec.Command("dpkg", "-l").Output(); err == nil {
			lines := strings.Split(string(out), "\n")
			count := 0
			for _, line := range lines {
				if strings.HasPrefix(line, "ii ") {
					count++
				}
			}
			sb.WriteString(fmt.Sprintf("Debian/Ubuntu packages: %d installed\n", count))
		}
	}

	if _, err := exec.LookPath("rpm"); err == nil {
		if out, err := exec.Command("rpm", "-qa").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			sb.WriteString(fmt.Sprintf("RPM packages: %d installed\n", len(lines)))
		}
	}

	if _, err := exec.LookPath("pacman"); err == nil {
		if out, err := exec.Command("pacman", "-Q").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			sb.WriteString(fmt.Sprintf("Pacman packages: %d installed\n", len(lines)))
		}
	}

	if _, err := exec.LookPath("brew"); err == nil {
		if out, err := exec.Command("brew", "list", "--formula").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			sb.WriteString(fmt.Sprintf("Homebrew formulae: %d installed\n", len(lines)))
		}
	}

	if _, err := exec.LookPath("snap"); err == nil {
		if out, err := exec.Command("snap", "list").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) > 1 {
				sb.WriteString(fmt.Sprintf("Snap packages: %d installed\n", len(lines)-1))
			}
		}
	}

	if _, err := exec.LookPath("flatpak"); err == nil {
		if out, err := exec.Command("flatpak", "list", "--app").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			sb.WriteString(fmt.Sprintf("Flatpak apps: %d installed\n", len(lines)))
		}
	}

	binDirs := []string{"/usr/local/bin", filepath.Join(os.Getenv("HOME"), ".local/bin"), filepath.Join(os.Getenv("HOME"), "go/bin")}
	for _, dir := range binDirs {
		if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
			sb.WriteString(fmt.Sprintf("%s: %d binaries\n", dir, len(entries)))
		}
	}

	return sb.String()
}

func getServiceInfo() string {
	var sb strings.Builder

	if _, err := exec.LookPath("systemctl"); err == nil {
		if out, err := exec.Command("systemctl", "list-units", "--type=service", "--state=running", "--no-pager", "--no-legend").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			sb.WriteString(fmt.Sprintf("Systemd running services: %d\n", len(lines)))

			sb.WriteString("\nKey services:\n")
			keyServices := []string{"ssh", "docker", "nginx", "apache", "mysql", "postgres", "redis", "mongodb"}
			for _, svc := range keyServices {
				for _, line := range lines {
					if strings.Contains(strings.ToLower(line), svc) {
						parts := strings.Fields(line)
						if len(parts) > 0 {
							sb.WriteString(fmt.Sprintf("  %s: running\n", parts[0]))
						}
						break
					}
				}
			}
		}
	}

	if _, err := exec.LookPath("service"); err == nil {
		if out, err := exec.Command("service", "--status-all").Output(); err == nil {
			lines := strings.Split(string(out), "\n")
			running := 0
			for _, line := range lines {
				if strings.Contains(line, "[ + ]") {
					running++
				}
			}
			sb.WriteString(fmt.Sprintf("SysV services running: %d\n", running))
		}
	}

	return sb.String()
}

func formatDocResult(doc *db.Doc) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[Cached: %s from %s]\n", doc.Name, doc.Source))
	if doc.Summary != "" {
		sb.WriteString(fmt.Sprintf("Summary: %s\n", doc.Summary))
	}
	sb.WriteString(fmt.Sprintf("Fetched: %s ago\n\n", time.Since(doc.FetchedAt).Truncate(time.Minute)))
	sb.WriteString(doc.Content)
	return sb.String()
}

func generateSummary(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) > 20 && len(line) < 200 && !strings.HasPrefix(line, "#") {
			return line
		}
	}
	if len(content) > 100 {
		return content[:100] + "..."
	}
	return ""
}

func stripANSI(s string) string {
	re := regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)
	return re.ReplaceAllString(s, "")
}

func stripHTML(s string) string {
	re := regexp.MustCompile(`<[^>]*>`)
	s = re.ReplaceAllString(s, "")

	s = strings.ReplaceAll(s, "&nbsp;", " ")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&quot;", "\"")

	re = regexp.MustCompile(`\n\s*\n\s*\n`)
	s = re.ReplaceAllString(s, "\n\n")

	return s
}
