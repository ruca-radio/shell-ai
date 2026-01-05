# Shell-AI

A terminal assistant that actually does things. No more Googling commands.

```bash
q "find all large files over 100mb"
q "read my nginx config and tell me what's wrong"
q "run the tests in background and let me know when done"
```

## What It Does

Ask in plain English. The AI reads files, writes files, runs commands, and manages background tasks - autonomously deciding what tools to use based on your request.

```
$ q "what's eating my disk space?"

⚡ run_command

The largest directories are:
- /var/log (2.3GB) - Consider rotating logs
- ~/Downloads (8.1GB) - Safe to clean
- ~/.cache (1.2GB) - Can be cleared
```

## Install

### From Source

```bash
git clone https://github.com/ruca-radio/shell-ai.git
cd shell-ai
go build -o ~/.local/bin/q .
```

### Homebrew (Coming Soon)

```bash
brew install ruca-radio/tap/shell-ai
```

## Setup

Set your API key:

```bash
# OpenAI (default)
export OPENAI_API_KEY=sk-...

# Or OpenRouter (for Claude, DeepSeek, etc.)
export OPENROUTER_API_KEY=sk-or-...

# Or Ollama Cloud
export OLLAMA_API_KEY=...
```

## Usage

### Basic

```bash
q "your request here"
```

### With Model Selection

```bash
q -m claude-sonnet "explain this error"
q -m ollama-qwen "generate a bash script"
```

### Pipe Input

```bash
cat error.log | q "what's wrong?"
git diff | q "write a commit message"
ps aux | q "which process is using the most memory?"
```

### Interactive Mode

Just run `q` with no arguments to enter chat mode. Press Enter on an empty line to copy the last code block to clipboard.

## Supported Providers

| Provider | Models | API Key |
|----------|--------|---------|
| OpenAI | gpt-4o, gpt-4.1-nano | `OPENAI_API_KEY` |
| OpenRouter | Claude Sonnet, DeepSeek | `OPENROUTER_API_KEY` |
| Ollama Local | Any local model | None |
| Ollama Cloud | qwen3-coder, deepseek-v3 | `OLLAMA_API_KEY` |

## Available Tools

The AI can use these tools autonomously:

| Tool | Description |
|------|-------------|
| `read_file` | Read file contents |
| `write_file` | Create or overwrite files |
| `append_file` | Add content to existing files |
| `run_command` | Execute shell commands (30s timeout) |
| `run_background` | Long-running tasks (builds, servers) |
| `check_task` | Check background task status |
| `list_tasks` | List all background tasks |
| `kill_task` | Terminate a background task |
| `list_files` | Browse directories |
| `search_files` | Find files by pattern or content |
| `get_file_info` | Get file metadata |
| `git_status` | Show branch and changed files |
| `git_diff` | Show file changes |
| `git_log` | Show recent commits |
| `ssh_exec` | Run command on remote host via SSH |
| `ssh_upload` | Upload file via SFTP |
| `ssh_download` | Download file via SFTP |
| `ssh_hosts` | List ~/.ssh/config hosts |
| `ping_host` | Ping host with latency stats |
| `port_scan` | Scan ports on a host |
| `lan_scan` | Discover hosts on local network |
| `wake_on_lan` | Wake sleeping machine via WoL |
| `spawn_agent` | Spawn sub-agent for complex tasks |
| `list_agents` | List spawned agents and status |
| `get_agent_result` | Get result from completed agent |
| `wait_for_agent` | Wait for agent to complete |
| `cancel_agent` | Cancel a running agent |

## Examples

### File Operations

```bash
q "read my .bashrc"
q "add an alias for docker compose to my .zshrc"
q "create a python script that downloads images"
q "find all TODO comments in my code"
```

### Command Execution

```bash
q "list files modified today"
q "check if port 3000 is in use"
q "compress all images in this folder"
q "what's my public IP?"
```

### Background Tasks

```bash
q "run npm install in background"
q "start the dev server in background"
q "check task_1 status"
q "kill task_2"
```

### Git Operations

```bash
q "what changed since last commit?"
q "show me recent commits"
q "what's the git status?"
q "diff the staged changes"
```

### Code Questions

```bash
q "how do I reverse a string in rust?"
q "regex to validate email"
q "curl command to POST json"
```

### LAN & SSH Management

```bash
q "show my ssh hosts"
q "run df -h on my server"
q "ping 192.168.1.1"
q "scan ports on nas.local"
q "what devices are on my network?"
q "wake up my desktop" # requires MAC in command or asks
q "upload config.yaml to server:/etc/app/"
q "download logs from server:/var/log/app.log"
```

### Sub-Agents (Parallel AI Workers)

Shell-AI can spawn autonomous sub-agents to work on complex tasks in parallel:

```bash
q "analyze all the Python files in this project and suggest improvements"
# Main AI spawns agents to analyze different parts concurrently

q "research how to implement OAuth2 in Go and write me a summary"
# Spawns a researcher agent while main AI continues

q "review this codebase for security issues"
# Agent autonomously reads files, analyzes patterns, reports findings
```

Agents have access to all tools (file ops, commands, SSH, etc.) but cannot spawn other agents. They work in background and report results when done.

## Configuration

Config lives at `~/.shell-ai/config.yaml`. Run `q config` to open the settings menu.

### Adding Custom Models

```yaml
models:
  - name: my-model
    model_name: actual-model-name
    endpoint: https://api.example.com/v1/chat/completions
    auth_env_var: MY_API_KEY
    prompt:
      - role: system
        content: You are a helpful assistant.
```

## Persistent Memory

Shell-AI remembers past conversations per directory. Context from previous sessions is automatically injected when relevant. Data stored in `~/.shell-ai/memory.db` (SQLite).

## Key Bindings

| Key | Action |
|-----|--------|
| `Enter` | Submit / Copy code to clipboard |
| `Ctrl+C` | Quit |
| `Ctrl+D` | Quit |
| `Esc` | Quit |

## How It Works

1. Your request goes to the selected LLM with available tool definitions
2. The AI decides which tools to use (if any)
3. Tools are executed and results returned to the AI
4. The AI formulates a response based on the results
5. Conversations are persisted to SQLite for contextual recall

Tool calling is supported on OpenAI and OpenRouter models. Ollama models receive tool descriptions in the prompt but cannot autonomously execute them.

## Credits

Originally created by [@ilanbigio](https://github.com/ibigio). This fork adds autonomous tool execution, multi-provider support, and persistent memory.

## License

MIT
