package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/signal"
	"q/config"
	"q/llm"
	. "q/types"
	"q/util"
	"runtime"
	"strings"
	"syscall"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

type State int

const (
	Loading State = iota
	ReceivingInput
	ReceivingResponse
)

type model struct {
	client           *llm.LLMClient
	modelName        string
	markdownRenderer *glamour.TermRenderer

	textInput textinput.Model
	spinner   spinner.Model

	state                    State
	query                    string
	latestCommandResponse    string
	latestCommandIsCode      bool
	formattedPartialResponse string
	toolActivity             string

	maxWidth    int
	runWithArgs bool
	err         error
}

type responseMsg struct {
	response string
	err      error
}

type partialResponseMsg struct {
	content string
	err     error
}

type toolActivityMsg struct {
	tool string
	args string
}

func makeQuery(client *llm.LLMClient, query string) tea.Cmd {
	return func() tea.Msg {
		response, err := client.Query(query)
		return responseMsg{response: response, err: err}
	}
}

func (m model) handleKeyEnter() (tea.Model, tea.Cmd) {
	if m.state != ReceivingInput {
		return m, nil
	}
	v := m.textInput.Value()

	if v == "" {
		if m.latestCommandResponse == "" {
			return m, tea.Quit
		}
		err := clipboard.WriteAll(m.latestCommandResponse)
		if err != nil {
			return m, tea.Quit
		}
		placeholderStyle := lipgloss.NewStyle().Faint(true)
		message := placeholderStyle.Render("Copied to clipboard.")
		return m, tea.Sequence(tea.Printf("%s", message), tea.Quit)
	}

	m.textInput.SetValue("")
	m.query = v
	m.state = Loading
	m.toolActivity = ""
	placeholderStyle := lipgloss.NewStyle().Faint(true).Width(m.maxWidth)
	message := placeholderStyle.Render(fmt.Sprintf("> %s", v))
	return m, tea.Sequence(tea.Printf("%s", message), tea.Batch(m.spinner.Tick, makeQuery(m.client, m.query)))
}

func (m model) formatResponse(response string, isCode bool) (string, error) {
	formatted, err := m.markdownRenderer.Render(response)
	if err != nil {
		return response, nil
	}

	formatted = strings.TrimPrefix(formatted, "\n")
	formatted = strings.TrimSuffix(formatted, "\n")

	if isCode {
		codeStyle := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1)
		formatted = codeStyle.Render(formatted)
	} else {
		formatted = "\n" + formatted
	}
	return formatted, nil
}

func (m model) getConnectionError(err error) string {
	styleRed := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleGreen := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleDim := lipgloss.NewStyle().Faint(true).Width(m.maxWidth).PaddingLeft(2)

	message := fmt.Sprintf("\n  %v\n\n%v\n",
		styleRed.Render(fmt.Sprintf("Error connecting to %s", m.modelName)),
		styleDim.Render(err.Error()))

	if strings.Contains(err.Error(), "connection refused") {
		if strings.Contains(strings.ToLower(m.modelName), "ollama") {
			message += fmt.Sprintf("\n  %v %v\n",
				styleGreen.Render("Hint:"),
				"Run 'ollama serve' to start the local server")
		}
	}

	return message
}

func (m model) handleResponseMsg(msg responseMsg) (tea.Model, tea.Cmd) {
	m.formattedPartialResponse = ""
	m.toolActivity = ""

	if msg.err != nil {
		m.state = ReceivingInput
		message := m.getConnectionError(msg.err)
		return m, tea.Sequence(tea.Printf("%s", message), textinput.Blink)
	}

	content, isOnlyCode := util.ExtractFirstCodeBlock(msg.response)
	if content != "" {
		m.latestCommandResponse = content
	}

	formatted, _ := m.formatResponse(msg.response, util.StartsWithCodeBlock(msg.response))

	m.textInput.Placeholder = "Ask anything... (ENTER to copy, Ctrl+C to quit)"
	if m.latestCommandResponse != "" {
		m.textInput.Placeholder = "Follow up... (ENTER to copy code, Ctrl+C to quit)"
	}

	m.state = ReceivingInput
	m.latestCommandIsCode = isOnlyCode
	return m, tea.Sequence(tea.Printf("%s", formatted), textinput.Blink)
}

func (m model) handlePartialResponseMsg(msg partialResponseMsg) (tea.Model, tea.Cmd) {
	m.state = ReceivingResponse
	isCode := util.StartsWithCodeBlock(msg.content)
	formatted, _ := m.formatResponse(msg.content, isCode)
	m.formattedPartialResponse = formatted
	return m, nil
}

func (m model) handleToolActivityMsg(msg toolActivityMsg) (tea.Model, tea.Cmd) {
	toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	m.toolActivity = toolStyle.Render(fmt.Sprintf("⚡ %s", msg.tool))
	return m, nil
}

func (m model) Init() tea.Cmd {
	if m.runWithArgs {
		return tea.Batch(m.spinner.Tick, makeQuery(m.client, m.query))
	}
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc, tea.KeyCtrlD:
			return m, tea.Quit
		case tea.KeyEnter:
			return m.handleKeyEnter()
		}

	case responseMsg:
		return m.handleResponseMsg(msg)

	case partialResponseMsg:
		return m.handlePartialResponseMsg(msg)

	case toolActivityMsg:
		return m.handleToolActivityMsg(msg)

	case error:
		m.err = msg
		return m, nil
	}

	switch m.state {
	case Loading:
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case ReceivingInput:
		m.textInput, cmd = m.textInput.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m model) renderStatusBar() string {
	modelStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("62")).
		Foreground(lipgloss.Color("230")).
		Padding(0, 1)

	return modelStyle.Render(m.modelName)
}

func (m model) View() string {
	statusBar := m.renderStatusBar()

	switch m.state {
	case Loading:
		if m.toolActivity != "" {
			return statusBar + " " + m.toolActivity + "\n" + m.spinner.View()
		}
		return statusBar + "\n" + m.spinner.View()
	case ReceivingInput:
		return statusBar + "\n" + m.textInput.View()
	case ReceivingResponse:
		return statusBar + "\n" + m.formattedPartialResponse + "\n"
	}
	return ""
}

func initialModel(prompt string, client *llm.LLMClient, modelName string) model {
	maxWidth := util.GetTermSafeMaxWidth()
	ti := textinput.New()
	ti.Placeholder = "Ask anything..."
	ti.Focus()
	ti.Width = maxWidth

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	runWithArgs := prompt != ""

	r, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(int(maxWidth)),
	)

	m := model{
		client:                client,
		modelName:             modelName,
		markdownRenderer:      r,
		textInput:             ti,
		spinner:               s,
		state:                 ReceivingInput,
		query:                 "",
		latestCommandResponse: "",
		latestCommandIsCode:   false,
		maxWidth:              maxWidth,
		runWithArgs:           false,
		err:                   nil,
	}

	if runWithArgs {
		m.runWithArgs = true
		m.state = Loading
		m.query = prompt
	}
	return m
}

func printAPIKeyNotSetMessage(modelConfig ModelConfig) {
	r, _ := glamour.NewTermRenderer(glamour.WithAutoStyle())

	profileScriptName := ".zshrc or .bashrc"
	envVar := modelConfig.Auth
	if envVar == "" {
		envVar = "API_KEY"
	}

	shellSyntax := fmt.Sprintf("\n```bash\nexport %s=[your key]\n```", envVar)
	if runtime.GOOS == "windows" {
		profileScriptName = "$profile"
		shellSyntax = fmt.Sprintf("\n```powershell\n$env:%s = \"[your key]\"\n```", envVar)
	}

	styleRed := lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	msg1 := styleRed.Render(fmt.Sprintf("%s not set.", envVar))

	var helpURL string
	switch {
	case strings.Contains(envVar, "OPENAI"):
		helpURL = "https://platform.openai.com/api-keys"
	case strings.Contains(envVar, "OPENROUTER"):
		helpURL = "https://openrouter.ai/keys"
	case strings.Contains(envVar, "OLLAMA"):
		helpURL = "https://ollama.com/settings/keys"
	default:
		helpURL = "your provider's dashboard"
	}

	messageString := fmt.Sprintf(`
Get your API key from %s

Set it:
%s

Add to %s for persistence.`, helpURL, shellSyntax, profileScriptName)

	msg2, _ := r.Render(messageString)
	fmt.Printf("\n  %v%v\n", msg1, msg2)
}

func streamHandler(p *tea.Program) func(content string, err error) {
	return func(content string, err error) {
		p.Send(partialResponseMsg{content, err})
	}
}

func toolHandler(p *tea.Program) func(tool string, args string) {
	return func(tool string, args string) {
		p.Send(toolActivityMsg{tool, args})
	}
}

func getModelConfig(appConfig config.AppConfig, requestedModel string) (ModelConfig, error) {
	if len(appConfig.Models) == 0 {
		return ModelConfig{}, fmt.Errorf("no models configured")
	}

	targetModel := appConfig.Preferences.DefaultModel
	if requestedModel != "" {
		targetModel = requestedModel
	}

	for _, model := range appConfig.Models {
		if model.Name == targetModel {
			return model, nil
		}
	}

	if requestedModel != "" {
		var available []string
		for _, m := range appConfig.Models {
			available = append(available, m.Name)
		}
		return ModelConfig{}, fmt.Errorf("model '%s' not found. Available: %s", requestedModel, strings.Join(available, ", "))
	}

	return appConfig.Models[0], nil
}

func readStdin() string {
	stat, _ := os.Stdin.Stat()
	if (stat.Mode() & os.ModeCharDevice) == 0 {
		reader := bufio.NewReader(os.Stdin)
		var builder strings.Builder
		for {
			b, err := reader.ReadByte()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			builder.WriteByte(b)
		}
		return builder.String()
	}
	return ""
}

func runWatchMode() {
	appConfig, err := config.LoadAppConfig()
	if err != nil {
		config.PrintConfigErrorMessage(err)
		os.Exit(1)
	}

	modelConfig, err := getModelConfig(appConfig, modelFlag)
	if err != nil {
		config.PrintConfigErrorMessage(err)
		os.Exit(1)
	}

	if modelConfig.Auth != "" {
		envKey := modelConfig.Auth
		val := os.Getenv(envKey)
		if val == "" {
			printAPIKeyNotSetMessage(modelConfig)
			os.Exit(1)
		}
		modelConfig.Auth = val
	}

	c := llm.NewLLMClient(modelConfig)
	defer c.Close()

	styleGreen := lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleYellow := lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleDim := lipgloss.NewStyle().Faint(true)

	fmt.Println(styleGreen.Render("Shell-AI Watch Mode"))
	fmt.Println(styleDim.Render("==================="))
	fmt.Println()
	fmt.Println("Monitoring for errors and auto-repairing...")
	fmt.Println(styleDim.Render("Press Ctrl+C to stop"))
	fmt.Println()

	response, err := c.Query("Start watching this project for errors. Use start_watch to begin monitoring. Detect the build command automatically.")
	if err != nil {
		fmt.Printf("Error starting watch: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(styleYellow.Render("Watch started:"))
	fmt.Println(response)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan

	fmt.Println()
	fmt.Println(styleDim.Render("Stopping watch mode..."))
	c.Query("Stop watching. Use stop_watch.")
	fmt.Println(styleGreen.Render("Watch mode stopped."))
}

func runQProgram(prompt string) {
	appConfig, err := config.LoadAppConfig()
	if err != nil {
		config.PrintConfigErrorMessage(err)
		os.Exit(1)
	}

	modelConfig, err := getModelConfig(appConfig, modelFlag)
	if err != nil {
		config.PrintConfigErrorMessage(err)
		os.Exit(1)
	}

	if modelConfig.Auth != "" {
		envKey := modelConfig.Auth
		val := os.Getenv(envKey)
		if val == "" {
			printAPIKeyNotSetMessage(modelConfig)
			os.Exit(1)
		}
		modelConfig.Auth = val
		if modelConfig.OrgID != "" {
			modelConfig.OrgID = os.Getenv(modelConfig.OrgID)
		}
	}

	stdinData := readStdin()
	if stdinData != "" {
		if prompt != "" {
			prompt = fmt.Sprintf("Here's some input:\n```\n%s\n```\n\n%s", stdinData, prompt)
		} else {
			prompt = fmt.Sprintf("Here's some input:\n```\n%s\n```\n\nWhat would you like me to do with this?", stdinData)
		}
	}

	config.SaveAppConfig(appConfig)

	c := llm.NewLLMClient(modelConfig)
	defer c.Close()

	// Detect if running in interactive mode (no args and stdin is a terminal)
	stat, _ := os.Stdin.Stat()
	isStdinTerminal := (stat.Mode() & os.ModeCharDevice) != 0
	isInteractive := prompt == "" && isStdinTerminal

	if isInteractive {
		// Interactive mode: use bubbletea TUI
		p := tea.NewProgram(initialModel(prompt, c, modelConfig.Name))
		c.StreamCallback = streamHandler(p)
		c.ToolCallback = toolHandler(p)

		if _, err := p.Run(); err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
	} else {
		// Non-interactive mode: direct execution without TUI
		response, err := c.Query(prompt)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(response)
	}
}

var modelFlag string
var watchFlag bool

var RootCmd = &cobra.Command{
	Use:   "q [request]",
	Short: "AI terminal assistant",
	Long:  `Shell-AI: Ask questions, run commands, read/write files - all through natural language.`,
	Run: func(cmd *cobra.Command, args []string) {
		prompt := strings.Join(args, " ")
		if len(args) > 0 && args[0] == "config" {
			config.RunConfigProgram(args)
			return
		}
		if watchFlag {
			runWatchMode()
			return
		}
		runQProgram(prompt)
	},
}

func init() {
	RootCmd.Flags().StringVarP(&modelFlag, "model", "m", "", "Model to use (e.g., gpt-4o, claude-sonnet, ollama-qwen)")
	RootCmd.Flags().BoolVarP(&watchFlag, "watch", "w", false, "Start in self-healing watch mode")
}
