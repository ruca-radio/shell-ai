#!/usr/bin/env bash
set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info() { echo -e "${BLUE}==>${NC} $1"; }
success() { echo -e "${GREEN}==>${NC} $1"; }
warn() { echo -e "${YELLOW}==>${NC} $1"; }
error() { echo -e "${RED}ERROR:${NC} $1"; exit 1; }

INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
DATA_DIR="${DATA_DIR:-$HOME/.shell-ai}"
REPO_URL="https://github.com/ruca-radio/shell-ai.git"
MIN_GO_VERSION="1.21"

check_go() {
    if ! command -v go &> /dev/null; then
        error "Go is not installed. Please install Go $MIN_GO_VERSION or later.
    
Install Go:
  - macOS:   brew install go
  - Ubuntu:  sudo apt install golang-go
  - Fedora:  sudo dnf install golang
  - Arch:    sudo pacman -S go
  - Manual:  https://go.dev/dl/"
    fi

    GO_VERSION=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | sed 's/go//')
    MAJOR=$(echo "$GO_VERSION" | cut -d. -f1)
    MINOR=$(echo "$GO_VERSION" | cut -d. -f2)
    REQ_MAJOR=$(echo "$MIN_GO_VERSION" | cut -d. -f1)
    REQ_MINOR=$(echo "$MIN_GO_VERSION" | cut -d. -f2)
    
    if [[ "$MAJOR" -lt "$REQ_MAJOR" ]] || [[ "$MAJOR" -eq "$REQ_MAJOR" && "$MINOR" -lt "$REQ_MINOR" ]]; then
        error "Go version $GO_VERSION is too old. Please upgrade to $MIN_GO_VERSION or later."
    fi
    success "Go $GO_VERSION detected"
}

check_git() {
    if ! command -v git &> /dev/null; then
        error "Git is not installed. Please install git first."
    fi
    success "Git detected"
}

create_directories() {
    info "Creating directories..."
    
    mkdir -p "$INSTALL_DIR"
    mkdir -p "$DATA_DIR"
    
    chmod 755 "$INSTALL_DIR"
    chmod 700 "$DATA_DIR"
    
    success "Directories created"
    echo "    Binary:  $INSTALL_DIR"
    echo "    Data:    $DATA_DIR"
}

build_shell_ai() {
    info "Building shell-ai..."
    
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    
    if [[ -f "$SCRIPT_DIR/go.mod" ]]; then
        BUILD_DIR="$SCRIPT_DIR"
        info "Building from local source"
    else
        BUILD_DIR=$(mktemp -d)
        trap "rm -rf $BUILD_DIR" EXIT
        info "Cloning repository..."
        git clone --depth 1 "$REPO_URL" "$BUILD_DIR"
    fi
    
    cd "$BUILD_DIR"
    
    info "Downloading dependencies..."
    go mod download
    
    info "Compiling..."
    CGO_ENABLED=0 go build -ldflags="-s -w" -o "$INSTALL_DIR/q" .
    
    chmod 755 "$INSTALL_DIR/q"
    
    success "Binary installed to $INSTALL_DIR/q"
}

setup_path() {
    if [[ ":$PATH:" == *":$INSTALL_DIR:"* ]]; then
        success "$INSTALL_DIR already in PATH"
        return
    fi
    
    warn "$INSTALL_DIR is not in your PATH"
    
    SHELL_NAME=$(basename "$SHELL")
    case "$SHELL_NAME" in
        bash)
            RC_FILE="$HOME/.bashrc"
            ;;
        zsh)
            RC_FILE="$HOME/.zshrc"
            ;;
        fish)
            RC_FILE="$HOME/.config/fish/config.fish"
            ;;
        *)
            RC_FILE=""
            ;;
    esac
    
    if [[ -n "$RC_FILE" && -f "$RC_FILE" ]]; then
        if ! grep -q "Shell-AI" "$RC_FILE" 2>/dev/null; then
            echo "" >> "$RC_FILE"
            echo "# Shell-AI" >> "$RC_FILE"
            if [[ "$SHELL_NAME" == "fish" ]]; then
                echo "set -gx PATH \$PATH $INSTALL_DIR" >> "$RC_FILE"
            else
                echo "export PATH=\"\$PATH:$INSTALL_DIR\"" >> "$RC_FILE"
            fi
            success "Added $INSTALL_DIR to PATH in $RC_FILE"
        fi
        warn "Run 'source $RC_FILE' or restart your shell"
    else
        warn "Add this to your shell config:"
        echo "    export PATH=\"\$PATH:$INSTALL_DIR\""
    fi
}

setup_api_key() {
    echo ""
    info "API Key Setup"
    echo ""
    echo "Shell-AI requires an API key from one of these providers:"
    echo ""
    echo "  1. OpenAI (default)     - export OPENAI_API_KEY=sk-..."
    echo "  2. OpenRouter           - export OPENROUTER_API_KEY=sk-or-..."
    echo "  3. Ollama Cloud         - export OLLAMA_API_KEY=..."
    echo "  4. Ollama Local         - No key needed (run 'ollama serve')"
    echo ""
    
    if [[ -n "${OPENAI_API_KEY:-}" ]]; then
        success "OPENAI_API_KEY is already set"
    elif [[ -n "${OPENROUTER_API_KEY:-}" ]]; then
        success "OPENROUTER_API_KEY is already set"
    elif [[ -n "${OLLAMA_API_KEY:-}" ]]; then
        success "OLLAMA_API_KEY is already set"
    else
        warn "No API key detected. Set one of the above before using shell-ai."
    fi
}

print_usage() {
    echo ""
    echo -e "${GREEN}Installation complete!${NC}"
    echo ""
    echo "Usage:"
    echo "  q \"your question here\"     # Ask anything"
    echo "  q -m claude-sonnet \"...\"   # Use specific model"
    echo "  q --watch                   # Start self-healing mode"
    echo "  q config                    # Configure models"
    echo ""
    echo "Examples:"
    echo "  q \"find large files over 100mb\""
    echo "  q \"read my nginx config and fix errors\""
    echo "  q \"what's eating my disk space?\""
    echo "  cat error.log | q \"what's wrong?\""
    echo ""
    echo "Documentation: https://github.com/ruca-radio/shell-ai"
    echo ""
}

main() {
    echo ""
    echo -e "${BLUE}Shell-AI Installer${NC}"
    echo "=================="
    echo ""
    
    check_git
    check_go
    create_directories
    build_shell_ai
    setup_path
    setup_api_key
    print_usage
}

main "$@"
