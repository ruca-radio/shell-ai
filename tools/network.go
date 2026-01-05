package tools

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-ping/ping"
	"github.com/kevinburke/ssh_config"
	"github.com/melbahja/goph"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

var NetworkTools = []Tool{
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "ssh_exec",
			Description: "Execute a command on a remote host via SSH. Supports ~/.ssh/config aliases.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"host": {"type": "string", "description": "Hostname, IP, or SSH config alias"},
					"command": {"type": "string", "description": "Command to execute"},
					"user": {"type": "string", "description": "Username (optional if in ssh config)"},
					"port": {"type": "integer", "description": "SSH port (default 22)"},
					"key_path": {"type": "string", "description": "Path to private key (optional)"}
				},
				"required": ["host", "command"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "ssh_upload",
			Description: "Upload a file to a remote host via SFTP.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"host": {"type": "string", "description": "Hostname, IP, or SSH config alias"},
					"local_path": {"type": "string", "description": "Local file path"},
					"remote_path": {"type": "string", "description": "Remote destination path"},
					"user": {"type": "string", "description": "Username (optional)"}
				},
				"required": ["host", "local_path", "remote_path"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "ssh_download",
			Description: "Download a file from a remote host via SFTP.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"host": {"type": "string", "description": "Hostname, IP, or SSH config alias"},
					"remote_path": {"type": "string", "description": "Remote file path"},
					"local_path": {"type": "string", "description": "Local destination path"},
					"user": {"type": "string", "description": "Username (optional)"}
				},
				"required": ["host", "remote_path", "local_path"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "ping_host",
			Description: "Ping a host to check if it's reachable and measure latency.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"host": {"type": "string", "description": "Hostname or IP to ping"},
					"count": {"type": "integer", "description": "Number of pings (default 3)"}
				},
				"required": ["host"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "port_scan",
			Description: "Scan common ports on a host to see which services are running.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"host": {"type": "string", "description": "Hostname or IP to scan"},
					"ports": {"type": "string", "description": "Comma-separated ports or 'common' (default)"}
				},
				"required": ["host"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "lan_scan",
			Description: "Scan local network for active hosts. Requires network interface or CIDR.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"cidr": {"type": "string", "description": "CIDR range (e.g., 192.168.1.0/24). Auto-detects if empty."}
				},
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "wake_on_lan",
			Description: "Send Wake-on-LAN magic packet to wake a sleeping machine.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"mac": {"type": "string", "description": "MAC address (e.g., 00:11:22:33:44:55)"},
					"broadcast": {"type": "string", "description": "Broadcast address (default 255.255.255.255)"}
				},
				"required": ["mac"],
				"additionalProperties": false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFunction{
			Name:        "ssh_hosts",
			Description: "List configured SSH hosts from ~/.ssh/config.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {},
				"additionalProperties": false
			}`),
		},
	},
}

func init() {
	AvailableTools = append(AvailableTools, NetworkTools...)
}

func resolveSSHConfig(alias string) (hostname string, port int, username string, keyPath string) {
	hostname = alias
	port = 22
	username = ""
	keyPath = ""

	usr, err := user.Current()
	if err != nil {
		return
	}

	configPath := filepath.Join(usr.HomeDir, ".ssh", "config")
	f, err := os.Open(configPath)
	if err != nil {
		return
	}
	defer f.Close()

	cfg, err := ssh_config.Decode(f)
	if err != nil {
		return
	}

	if h, err := cfg.Get(alias, "Hostname"); err == nil && h != "" {
		hostname = h
	}
	if p, err := cfg.Get(alias, "Port"); err == nil && p != "" {
		fmt.Sscanf(p, "%d", &port)
	}
	if u, err := cfg.Get(alias, "User"); err == nil && u != "" {
		username = u
	}
	if k, err := cfg.Get(alias, "IdentityFile"); err == nil && k != "" {
		keyPath = expandPath(k)
	}

	return
}

func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		usr, err := user.Current()
		if err == nil {
			path = filepath.Join(usr.HomeDir, path[2:])
		}
	}
	return path
}

func getDefaultKeyPath() string {
	usr, err := user.Current()
	if err != nil {
		return ""
	}
	candidates := []string{"id_ed25519", "id_rsa", "id_ecdsa"}
	for _, name := range candidates {
		path := filepath.Join(usr.HomeDir, ".ssh", name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}

func createSSHClient(host string, username string, port int, keyPath string) (*goph.Client, error) {
	resolvedHost, resolvedPort, resolvedUser, resolvedKey := resolveSSHConfig(host)

	if username == "" {
		username = resolvedUser
	}
	if username == "" {
		usr, _ := user.Current()
		if usr != nil {
			username = usr.Username
		}
	}
	if port == 0 {
		port = resolvedPort
	}
	if keyPath == "" {
		keyPath = resolvedKey
	}
	if keyPath == "" {
		keyPath = getDefaultKeyPath()
	}

	var auth goph.Auth
	var err error

	if keyPath != "" {
		auth, err = goph.Key(keyPath, "")
		if err != nil {
			return nil, fmt.Errorf("failed to load key %s: %w", keyPath, err)
		}
	} else {
		return nil, fmt.Errorf("no SSH key found. Specify key_path or add IdentityFile to ~/.ssh/config")
	}

	client, err := goph.NewConn(&goph.Config{
		User:     username,
		Addr:     resolvedHost,
		Port:     uint(port),
		Auth:     auth,
		Timeout:  10 * time.Second,
		Callback: ssh.InsecureIgnoreHostKey(),
	})

	return client, err
}

func sshExec(args map[string]interface{}) (string, error) {
	host, _ := args["host"].(string)
	command, _ := args["command"].(string)
	username, _ := args["user"].(string)
	keyPath, _ := args["key_path"].(string)

	port := 22
	if p, ok := args["port"].(float64); ok {
		port = int(p)
	}

	if host == "" || command == "" {
		return "", fmt.Errorf("host and command required")
	}

	client, err := createSSHClient(host, username, port, keyPath)
	if err != nil {
		return "", err
	}
	defer client.Close()

	output, err := client.Run(command)
	if err != nil {
		return string(output) + "\n[Error: " + err.Error() + "]", nil
	}

	return string(output), nil
}

func sshUpload(args map[string]interface{}) (string, error) {
	host, _ := args["host"].(string)
	localPath, _ := args["local_path"].(string)
	remotePath, _ := args["remote_path"].(string)
	username, _ := args["user"].(string)

	if host == "" || localPath == "" || remotePath == "" {
		return "", fmt.Errorf("host, local_path, and remote_path required")
	}

	localPath = expandPath(localPath)

	client, err := createSSHClient(host, username, 0, "")
	if err != nil {
		return "", err
	}
	defer client.Close()

	sftpClient, err := sftp.NewClient(client.Client)
	if err != nil {
		return "", fmt.Errorf("SFTP connection failed: %w", err)
	}
	defer sftpClient.Close()

	localFile, err := os.Open(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to open local file: %w", err)
	}
	defer localFile.Close()

	remoteFile, err := sftpClient.Create(remotePath)
	if err != nil {
		return "", fmt.Errorf("failed to create remote file: %w", err)
	}
	defer remoteFile.Close()

	written, err := remoteFile.ReadFrom(localFile)
	if err != nil {
		return "", fmt.Errorf("upload failed: %w", err)
	}

	return fmt.Sprintf("Uploaded %d bytes to %s:%s", written, host, remotePath), nil
}

func sshDownload(args map[string]interface{}) (string, error) {
	host, _ := args["host"].(string)
	remotePath, _ := args["remote_path"].(string)
	localPath, _ := args["local_path"].(string)
	username, _ := args["user"].(string)

	if host == "" || remotePath == "" || localPath == "" {
		return "", fmt.Errorf("host, remote_path, and local_path required")
	}

	localPath = expandPath(localPath)

	client, err := createSSHClient(host, username, 0, "")
	if err != nil {
		return "", err
	}
	defer client.Close()

	sftpClient, err := sftp.NewClient(client.Client)
	if err != nil {
		return "", fmt.Errorf("SFTP connection failed: %w", err)
	}
	defer sftpClient.Close()

	remoteFile, err := sftpClient.Open(remotePath)
	if err != nil {
		return "", fmt.Errorf("failed to open remote file: %w", err)
	}
	defer remoteFile.Close()

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return "", err
	}

	localFile, err := os.Create(localPath)
	if err != nil {
		return "", fmt.Errorf("failed to create local file: %w", err)
	}
	defer localFile.Close()

	written, err := localFile.ReadFrom(remoteFile)
	if err != nil {
		return "", fmt.Errorf("download failed: %w", err)
	}

	return fmt.Sprintf("Downloaded %d bytes from %s:%s to %s", written, host, remotePath, localPath), nil
}

func pingHost(args map[string]interface{}) (string, error) {
	host, _ := args["host"].(string)
	if host == "" {
		return "", fmt.Errorf("host required")
	}

	count := 3
	if c, ok := args["count"].(float64); ok {
		count = int(c)
	}

	pinger, err := ping.NewPinger(host)
	if err != nil {
		return "", fmt.Errorf("failed to create pinger: %w", err)
	}

	pinger.Count = count
	pinger.Timeout = time.Duration(count+2) * time.Second
	pinger.SetPrivileged(false)

	err = pinger.Run()
	if err != nil {
		return "", fmt.Errorf("ping failed: %w", err)
	}

	stats := pinger.Statistics()

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Ping %s (%s):\n", host, stats.IPAddr))
	result.WriteString(fmt.Sprintf("  Packets: %d sent, %d received, %.1f%% loss\n",
		stats.PacketsSent, stats.PacketsRecv, stats.PacketLoss))
	if stats.PacketsRecv > 0 {
		result.WriteString(fmt.Sprintf("  Latency: min=%.2fms avg=%.2fms max=%.2fms\n",
			float64(stats.MinRtt.Microseconds())/1000,
			float64(stats.AvgRtt.Microseconds())/1000,
			float64(stats.MaxRtt.Microseconds())/1000))
	}

	return result.String(), nil
}

var commonPorts = map[int]string{
	22: "SSH", 80: "HTTP", 443: "HTTPS", 21: "FTP", 23: "Telnet",
	25: "SMTP", 53: "DNS", 110: "POP3", 143: "IMAP", 3306: "MySQL",
	5432: "PostgreSQL", 6379: "Redis", 27017: "MongoDB", 8080: "HTTP-Alt",
	3389: "RDP", 5900: "VNC", 8443: "HTTPS-Alt", 9090: "Prometheus",
}

func portScan(args map[string]interface{}) (string, error) {
	host, _ := args["host"].(string)
	if host == "" {
		return "", fmt.Errorf("host required")
	}

	ports := []int{22, 80, 443, 21, 23, 25, 53, 110, 143, 3306, 5432, 6379, 8080, 3389, 5900}

	if portsStr, ok := args["ports"].(string); ok && portsStr != "" && portsStr != "common" {
		ports = []int{}
		for _, p := range strings.Split(portsStr, ",") {
			var port int
			if _, err := fmt.Sscanf(strings.TrimSpace(p), "%d", &port); err == nil {
				ports = append(ports, port)
			}
		}
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Port scan for %s:\n", host))

	var wg sync.WaitGroup
	results := make(chan string, len(ports))

	for _, port := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			address := fmt.Sprintf("%s:%d", host, p)
			conn, err := net.DialTimeout("tcp", address, 2*time.Second)
			if err == nil {
				conn.Close()
				service := commonPorts[p]
				if service == "" {
					service = "unknown"
				}
				results <- fmt.Sprintf("  %d/tcp open (%s)\n", p, service)
			}
		}(port)
	}

	wg.Wait()
	close(results)

	openCount := 0
	for r := range results {
		result.WriteString(r)
		openCount++
	}

	if openCount == 0 {
		result.WriteString("  No open ports found in scanned range\n")
	}

	return result.String(), nil
}

func getLocalCIDR() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}

	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				ip := ipnet.IP.To4()
				mask := ipnet.Mask
				network := net.IP(make([]byte, 4))
				for i := 0; i < 4; i++ {
					network[i] = ip[i] & mask[i]
				}
				ones, _ := mask.Size()
				return fmt.Sprintf("%s/%d", network.String(), ones)
			}
		}
	}
	return ""
}

func lanScan(args map[string]interface{}) (string, error) {
	cidr, _ := args["cidr"].(string)
	if cidr == "" {
		cidr = getLocalCIDR()
	}
	if cidr == "" {
		return "", fmt.Errorf("could not detect network. Please specify CIDR (e.g., 192.168.1.0/24)")
	}

	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("invalid CIDR: %w", err)
	}

	var hosts []string
	for ip := ip.Mask(ipnet.Mask); ipnet.Contains(ip); incrementIP(ip) {
		hosts = append(hosts, ip.String())
	}

	if len(hosts) > 256 {
		return "", fmt.Errorf("CIDR range too large (max /24). Got %d hosts", len(hosts))
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("Scanning %s (%d hosts)...\n", cidr, len(hosts)))

	var wg sync.WaitGroup
	results := make(chan string, len(hosts))
	sem := make(chan struct{}, 50)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for _, h := range hosts {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
				defer func() { <-sem }()
			}

			conn, err := net.DialTimeout("tcp", host+":22", 500*time.Millisecond)
			if err == nil {
				conn.Close()
				results <- fmt.Sprintf("  %s (SSH)\n", host)
				return
			}

			conn, err = net.DialTimeout("tcp", host+":80", 500*time.Millisecond)
			if err == nil {
				conn.Close()
				results <- fmt.Sprintf("  %s (HTTP)\n", host)
				return
			}

			conn, err = net.DialTimeout("tcp", host+":443", 500*time.Millisecond)
			if err == nil {
				conn.Close()
				results <- fmt.Sprintf("  %s (HTTPS)\n", host)
				return
			}
		}(h)
	}

	wg.Wait()
	close(results)

	found := 0
	for r := range results {
		result.WriteString(r)
		found++
	}

	result.WriteString(fmt.Sprintf("\nFound %d active hosts\n", found))
	return result.String(), nil
}

func incrementIP(ip net.IP) {
	for j := len(ip) - 1; j >= 0; j-- {
		ip[j]++
		if ip[j] > 0 {
			break
		}
	}
}

func wakeOnLan(args map[string]interface{}) (string, error) {
	macStr, _ := args["mac"].(string)
	if macStr == "" {
		return "", fmt.Errorf("MAC address required")
	}

	broadcast := "255.255.255.255"
	if b, ok := args["broadcast"].(string); ok && b != "" {
		broadcast = b
	}

	// Parse MAC address - accept formats like 00:11:22:33:44:55 or 00-11-22-33-44-55
	macStr = regexp.MustCompile(`[:-]`).ReplaceAllString(macStr, "")
	if len(macStr) != 12 {
		return "", fmt.Errorf("invalid MAC address format")
	}

	mac, err := hex.DecodeString(macStr)
	if err != nil {
		return "", fmt.Errorf("invalid MAC address: %w", err)
	}

	// Build magic packet: 6 bytes of 0xFF followed by MAC repeated 16 times
	packet := make([]byte, 102)
	for i := 0; i < 6; i++ {
		packet[i] = 0xFF
	}
	for i := 0; i < 16; i++ {
		copy(packet[6+i*6:], mac)
	}

	// Send via UDP broadcast on port 9
	addr, err := net.ResolveUDPAddr("udp", broadcast+":9")
	if err != nil {
		return "", fmt.Errorf("invalid broadcast address: %w", err)
	}

	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return "", fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	_, err = conn.Write(packet)
	if err != nil {
		return "", fmt.Errorf("failed to send WoL packet: %w", err)
	}

	return fmt.Sprintf("Wake-on-LAN packet sent to %s (broadcast: %s)", args["mac"], broadcast), nil
}

func sshHosts(args map[string]interface{}) (string, error) {
	usr, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("could not get user: %w", err)
	}

	configPath := filepath.Join(usr.HomeDir, ".ssh", "config")
	f, err := os.Open(configPath)
	if err != nil {
		return "No ~/.ssh/config found", nil
	}
	defer f.Close()

	cfg, err := ssh_config.Decode(f)
	if err != nil {
		return "", fmt.Errorf("failed to parse SSH config: %w", err)
	}

	var result strings.Builder
	result.WriteString("SSH Configured Hosts:\n")

	seen := make(map[string]bool)
	for _, host := range cfg.Hosts {
		for _, pattern := range host.Patterns {
			name := pattern.String()
			if name == "*" || strings.Contains(name, "?") || seen[name] {
				continue
			}
			seen[name] = true

			hostname, _ := cfg.Get(name, "Hostname")
			user, _ := cfg.Get(name, "User")
			port, _ := cfg.Get(name, "Port")

			if hostname == "" {
				hostname = name
			}
			if port == "" {
				port = "22"
			}

			result.WriteString(fmt.Sprintf("  %s", name))
			if name != hostname {
				result.WriteString(fmt.Sprintf(" -> %s", hostname))
			}
			if user != "" {
				result.WriteString(fmt.Sprintf(" (user: %s)", user))
			}
			if port != "22" {
				result.WriteString(fmt.Sprintf(" (port: %s)", port))
			}
			result.WriteString("\n")
		}
	}

	return result.String(), nil
}
