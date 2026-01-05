package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"q/types"
	"q/util"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

const listHeight = 14

var (
	styleRed          = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	styleGreen        = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	greyStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	titleStyle        = lipgloss.NewStyle().MarginLeft(2).Foreground(lipgloss.Color("240"))
	itemStyle         = lipgloss.NewStyle().PaddingLeft(4)
	selectedItemStyle = lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color("170"))
	paginationStyle   = list.DefaultStyles().PaginationStyle.PaddingLeft(4)
	helpStyle         = list.DefaultStyles().HelpStyle.PaddingLeft(4).PaddingBottom(1)
	quitTextStyle     = lipgloss.NewStyle().Faint(true).Margin(1, 0, 2, 4)
)

var providerPresets = []types.ProviderPreset{
	{Name: "OpenAI", Endpoint: "https://api.openai.com/v1/chat/completions", AuthEnvVar: "OPENAI_API_KEY", AuthHeader: "Authorization"},
	{Name: "OpenRouter", Endpoint: "https://openrouter.ai/api/v1/chat/completions", AuthEnvVar: "OPENROUTER_API_KEY", AuthHeader: "Authorization"},
	{Name: "Anthropic", Endpoint: "https://api.anthropic.com/v1/messages", AuthEnvVar: "ANTHROPIC_API_KEY", AuthHeader: "x-api-key"},
	{Name: "Ollama Local", Endpoint: "http://127.0.0.1:11434/v1/chat/completions", AuthEnvVar: "", AuthHeader: ""},
	{Name: "Ollama Cloud", Endpoint: "https://ollama.com/api/chat", AuthEnvVar: "OLLAMA_API_KEY", AuthHeader: "Authorization"},
	{Name: "Azure OpenAI", Endpoint: "https://YOUR-RESOURCE.openai.azure.com/openai/deployments/YOUR-DEPLOYMENT/chat/completions?api-version=2024-02-15-preview", AuthEnvVar: "AZURE_OPENAI_API_KEY", AuthHeader: "Api-Key"},
	{Name: "Groq", Endpoint: "https://api.groq.com/openai/v1/chat/completions", AuthEnvVar: "GROQ_API_KEY", AuthHeader: "Authorization"},
	{Name: "Together AI", Endpoint: "https://api.together.xyz/v1/chat/completions", AuthEnvVar: "TOGETHER_API_KEY", AuthHeader: "Authorization"},
	{Name: "Mistral AI", Endpoint: "https://api.mistral.ai/v1/chat/completions", AuthEnvVar: "MISTRAL_API_KEY", AuthHeader: "Authorization"},
	{Name: "Custom", Endpoint: "", AuthEnvVar: "", AuthHeader: "Authorization"},
}

type itemDelegate struct{}

func (d itemDelegate) Height() int                             { return 1 }
func (d itemDelegate) Spacing() int                            { return 0 }
func (d itemDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d itemDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	i, ok := listItem.(menuItem)
	if !ok {
		return
	}

	fn := itemStyle.Render
	if index == m.Index() {
		fn = func(s ...string) string { return selectedItemStyle.Render("> " + strings.Join(s, " ")) }
	}
	text := fn(i.title)
	if i.data != "" {
		text = fmt.Sprintf("%s %s", text, greyStyle.Render("("+i.data+")"))
	}

	fmt.Fprint(w, text)
}

type menuItem struct {
	title     string
	selectCmd tea.Cmd
	data      string
}

func (i menuItem) FilterValue() string { return i.title }

type menuFunc func(config AppConfig) list.Model

type page int

const (
	ListPage page = iota
	InputPage
)

type inputMode int

const (
	inputNone inputMode = iota
	inputText
)

type setMenuMsg struct{ menu menuFunc }
type backMsg struct{}
type quitMsg struct{}
type configSavedMsg struct{}
type editorFinishedMsg struct{ err error }
type setDefaultModelMsg struct{ model string }
type toggleBoolPrefMsg struct{ field string }
type deleteModelMsg struct{ modelName string }
type addModelMsg struct{ model types.ModelConfig }
type setInputModeMsg struct {
	prompt   string
	initial  string
	onSubmit func(string) tea.Cmd
}

type state struct {
	page      page
	menu      menuFunc
	listIndex int
}

type model struct {
	state         state
	list          list.Model
	backstack     []state
	appConfig     AppConfig
	quitting      bool
	inputMode     inputMode
	textInput     textinput.Model
	onInputSubmit func(string) tea.Cmd
	inputPrompt   string
}

func cmdSetMenu(menu menuFunc) tea.Cmd { return func() tea.Msg { return setMenuMsg{menu} } }
func cmdBack() tea.Cmd                 { return func() tea.Msg { return backMsg{} } }
func cmdQuit() tea.Cmd                 { return func() tea.Msg { return quitMsg{} } }
func cmdSetDefaultModel(model string) tea.Cmd {
	return func() tea.Msg { return setDefaultModelMsg{model} }
}
func cmdTogglePref(field string) tea.Cmd      { return func() tea.Msg { return toggleBoolPrefMsg{field} } }
func cmdDeleteModel(name string) tea.Cmd      { return func() tea.Msg { return deleteModelMsg{name} } }
func cmdAddModel(m types.ModelConfig) tea.Cmd { return func() tea.Msg { return addModelMsg{m} } }
func cmdSaveConfig(cfg AppConfig) tea.Cmd {
	return func() tea.Msg { SaveAppConfig(cfg); return configSavedMsg{} }
}
func cmdSetInput(prompt, initial string, onSubmit func(string) tea.Cmd) tea.Cmd {
	return func() tea.Msg { return setInputModeMsg{prompt: prompt, initial: initial, onSubmit: onSubmit} }
}

func openEditor() tea.Cmd {
	return func() tea.Msg {
		fullPath, err := FullFilePath(configFilePath)
		if err != nil {
			return editorFinishedMsg{err: err}
		}
		editor := os.Getenv("EDITOR")
		if editor == "" {
			editor = "vim"
		}
		cmd := exec.Command(editor, fullPath) //nolint:gosec
		if err := cmd.Run(); err != nil {
			return editorFinishedMsg{err: err}
		}
		return editorFinishedMsg{err: nil}
	}
}

func openBrowser(url string) tea.Cmd {
	return func() tea.Msg {
		util.OpenBrowser(url)
		return nil
	}
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.inputMode == inputText {
		return m.updateInput(msg)
	}

	switch msg := msg.(type) {
	case quitMsg:
		m.quitting = true
		return m, tea.Quit
	case backMsg:
		if len(m.backstack) > 0 {
			m.state = m.backstack[len(m.backstack)-1]
			m.backstack = m.backstack[:len(m.backstack)-1]
			m.list = m.state.menu(m.appConfig)
			m.list.Select(m.state.listIndex)
		}
		return m, nil
	case setMenuMsg:
		m.backstack = append(m.backstack, m.state)
		m.list = msg.menu(m.appConfig)
		m.state = state{page: ListPage, menu: msg.menu}
		return m, nil
	case setDefaultModelMsg:
		m.appConfig.Preferences.DefaultModel = msg.model
		return m, tea.Sequence(cmdSaveConfig(m.appConfig), cmdBack())
	case toggleBoolPrefMsg:
		switch msg.field {
		case "save_history":
			m.appConfig.Preferences.SaveHistory = !m.appConfig.Preferences.SaveHistory
		case "enable_knowledge":
			m.appConfig.Preferences.EnableKnowledge = !m.appConfig.Preferences.EnableKnowledge
		case "stream_responses":
			m.appConfig.Preferences.StreamResponses = !m.appConfig.Preferences.StreamResponses
		case "show_tool_activity":
			m.appConfig.Preferences.ShowToolActivity = !m.appConfig.Preferences.ShowToolActivity
		case "auto_copy_code":
			m.appConfig.Preferences.AutoCopyCode = !m.appConfig.Preferences.AutoCopyCode
		}
		SaveAppConfig(m.appConfig)
		m.list = m.state.menu(m.appConfig)
		return m, nil
	case deleteModelMsg:
		var newModels []types.ModelConfig
		for _, mm := range m.appConfig.Models {
			if mm.Name != msg.modelName {
				newModels = append(newModels, mm)
			}
		}
		m.appConfig.Models = newModels
		SaveAppConfig(m.appConfig)
		return m, cmdBack()
	case addModelMsg:
		m.appConfig.Models = append(m.appConfig.Models, msg.model)
		SaveAppConfig(m.appConfig)
		return m, cmdBack()
	case setInputModeMsg:
		m.inputMode = inputText
		m.inputPrompt = msg.prompt
		m.onInputSubmit = msg.onSubmit
		ti := textinput.New()
		ti.Placeholder = msg.prompt
		ti.SetValue(msg.initial)
		ti.Focus()
		ti.Width = 64
		m.textInput = ti
		return m, textinput.Blink
	case editorFinishedMsg:
		if msg.err == nil {
			if cfg, err := LoadAppConfig(); err == nil {
				m.appConfig = cfg
				m.list = m.state.menu(m.appConfig)
			}
		}
	}

	if km, ok := msg.(tea.KeyMsg); ok {
		switch km.Type {
		case tea.KeyCtrlC, tea.KeyCtrlD:
			return m, cmdQuit()
		case tea.KeyEsc:
			if len(m.backstack) > 0 {
				return m, cmdBack()
			}
			return m, cmdQuit()
		case tea.KeyEnter:
			i, _ := m.list.SelectedItem().(menuItem)
			if i.selectCmd != nil {
				return m, i.selectCmd
			}
		}
	}

	var cmd tea.Cmd
	if !m.quitting {
		m.list, cmd = m.list.Update(msg)
	}
	m.state.listIndex = m.list.Index()
	return m, cmd
}

func (m model) updateInput(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch km := msg.(type) {
	case tea.KeyMsg:
		switch km.Type {
		case tea.KeyEnter:
			value := strings.TrimSpace(m.textInput.Value())
			m.inputMode = inputNone
			if m.onInputSubmit != nil {
				return m, m.onInputSubmit(value)
			}
			return m, nil
		case tea.KeyEsc:
			m.inputMode = inputNone
			return m, nil
		case tea.KeyCtrlC:
			return m, cmdQuit()
		}
	}
	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m model) View() string {
	if m.quitting {
		return ""
	}
	if m.inputMode == inputText {
		return fmt.Sprintf("\n  %s\n\n  %s\n", m.inputPrompt, m.textInput.View())
	}
	return "\n" + m.list.View()
}

func defaultList(title string, items []menuItem) list.Model {
	listItems := make([]list.Item, len(items))
	for i, item := range items {
		listItems[i] = item
	}
	l := list.New(listItems, itemDelegate{}, 20, listHeight)
	l.Title = title
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)
	l.Styles.Title = titleStyle
	l.SetWidth(100)
	l.Styles.PaginationStyle = paginationStyle
	l.Styles.HelpStyle = helpStyle
	l.SetShowHelp(false)
	return l
}

func boolStatus(b bool) string {
	if b {
		return "ON"
	}
	return "OFF"
}

func mainMenu(appConfig AppConfig) list.Model {
	defaultModel := appConfig.Preferences.DefaultModel
	if defaultModel == "" && len(appConfig.Models) > 0 {
		defaultModel = appConfig.Models[0].Name
	}

	items := []menuItem{
		{title: "Default Model", data: defaultModel, selectCmd: cmdSetMenu(defaultModelSelectMenu)},
		{title: "Manage Models", data: fmt.Sprintf("%d configured", len(appConfig.Models)), selectCmd: cmdSetMenu(manageModelsMenu)},
		{title: "Add Provider / Model", selectCmd: cmdSetMenu(addModelProviderMenu)},
		{title: "Settings", selectCmd: cmdSetMenu(settingsMenu)},
		{title: "Edit Config File", data: "~/.shell-ai/config.yaml", selectCmd: openEditor()},
		{title: "Reset to Defaults", selectCmd: cmdSetMenu(resetConfirmMenu)},
		{title: "Documentation", selectCmd: openBrowser("https://github.com/ruca-radio/shell-ai")},
		{title: "Quit", data: "esc", selectCmd: cmdQuit()},
	}
	return defaultList("Shell-AI Settings", items)
}

func defaultModelSelectMenu(appConfig AppConfig) list.Model {
	var items []menuItem
	for _, m := range appConfig.Models {
		display := m.Name
		if display == "" {
			display = m.ModelName
		}
		marker := ""
		if m.Name == appConfig.Preferences.DefaultModel {
			marker = "✓"
		}
		items = append(items, menuItem{title: display, data: marker, selectCmd: tea.Sequence(cmdSetDefaultModel(m.Name), cmdBack())})
	}
	items = append(items, menuItem{title: "← Back", selectCmd: cmdBack()})
	return defaultList("Select Default Model", items)
}

func manageModelsMenu(appConfig AppConfig) list.Model {
	var items []menuItem
	for _, m := range appConfig.Models {
		display := m.Name
		if display == "" {
			display = m.ModelName
		}
		items = append(items, menuItem{title: display, data: truncateString(m.Endpoint, 40), selectCmd: cmdSetMenu(modelDetailsMenu(m))})
	}
	items = append(items, menuItem{title: "← Back", selectCmd: cmdBack()})
	return defaultList("Manage Models", items)
}

func modelDetailsMenu(mc types.ModelConfig) menuFunc {
	return func(appConfig AppConfig) list.Model {
		display := mc.Name
		if display == "" {
			display = mc.ModelName
		}
		authStatus := "None required"
		if mc.Auth != "" {
			if os.Getenv(mc.Auth) != "" {
				authStatus = mc.Auth + " ✓"
			} else {
				authStatus = mc.Auth + " (missing)"
			}
		}
		items := []menuItem{
			{title: "Name", data: display},
			{title: "Model ID", data: mc.ModelName},
			{title: "Endpoint", data: truncateString(mc.Endpoint, 40)},
			{title: "Auth Env Var", data: authStatus},
			{title: "Auth Header", data: mc.AuthHeader},
			{title: "Set as Default", selectCmd: tea.Sequence(cmdSetDefaultModel(mc.Name), cmdBack())},
			{title: "Delete Model", data: "permanent", selectCmd: cmdSetMenu(deleteModelConfirmMenu(mc.Name))},
			{title: "← Back", selectCmd: cmdBack()},
		}
		return defaultList("Model: "+display, items)
	}
}

func deleteModelConfirmMenu(name string) menuFunc {
	return func(appConfig AppConfig) list.Model {
		items := []menuItem{
			{title: "Yes, delete " + name, data: "cannot undo", selectCmd: cmdDeleteModel(name)},
			{title: "No, cancel", selectCmd: cmdBack()},
		}
		return defaultList("Delete model '"+name+"'?", items)
	}
}

func addModelProviderMenu(appConfig AppConfig) list.Model {
	var items []menuItem
	for _, preset := range providerPresets {
		p := preset
		items = append(items, menuItem{
			title:     p.Name,
			data:      p.AuthEnvVar,
			selectCmd: startAddModelWizard(p),
		})
	}
	items = append(items, menuItem{title: "← Back", selectCmd: cmdBack()})
	return defaultList("Select Provider", items)
}

func startAddModelWizard(preset types.ProviderPreset) tea.Cmd {
	return cmdSetInput("Display name (friendly)", preset.Name, func(display string) tea.Cmd {
		if display == "" {
			display = preset.Name
		}
		return cmdSetInput("Model ID (e.g., gpt-4o, claude-sonnet)", preset.Name, func(modelID string) tea.Cmd {
			if modelID == "" {
				modelID = preset.Name
			}
			return resolveEndpointStep(preset, display, modelID)
		})
	})
}

func resolveEndpointStep(preset types.ProviderPreset, name, modelID string) tea.Cmd {
	if strings.TrimSpace(preset.Endpoint) == "" {
		return cmdSetInput("Endpoint URL", "", func(ep string) tea.Cmd {
			ep = strings.TrimSpace(ep)
			if ep == "" {
				return cmdBack()
			}
			preset.Endpoint = ep
			return resolveAuthEnvStep(preset, name, modelID)
		})
	}
	return resolveAuthEnvStep(preset, name, modelID)
}

func resolveAuthEnvStep(preset types.ProviderPreset, name, modelID string) tea.Cmd {
	if preset.AuthEnvVar == "" && preset.Name != "Ollama Local" {
		return cmdSetInput("Auth env var (leave blank for none)", "", func(envVar string) tea.Cmd {
			preset.AuthEnvVar = strings.TrimSpace(envVar)
			return resolveAuthHeaderStep(preset, name, modelID)
		})
	}
	return resolveAuthHeaderStep(preset, name, modelID)
}

func resolveAuthHeaderStep(preset types.ProviderPreset, name, modelID string) tea.Cmd {
	if preset.AuthEnvVar != "" && preset.AuthHeader == "" {
		preset.AuthHeader = "Authorization"
	}
	newModel := types.ModelConfig{
		Name:       name,
		ModelName:  modelID,
		Endpoint:   preset.Endpoint,
		Auth:       preset.AuthEnvVar,
		AuthHeader: preset.AuthHeader,
		Prompt: []types.Message{{
			Role:    "system",
			Content: "You are a helpful terminal assistant. Be concise and direct.",
		}},
	}
	return cmdAddModel(newModel)
}

func settingsMenu(appConfig AppConfig) list.Model {
	items := []menuItem{
		{title: "Save Conversation History", data: boolStatus(appConfig.Preferences.SaveHistory), selectCmd: cmdTogglePref("save_history")},
		{title: "Enable Knowledge Graph", data: boolStatus(appConfig.Preferences.EnableKnowledge), selectCmd: cmdTogglePref("enable_knowledge")},
		{title: "Stream Responses", data: boolStatus(appConfig.Preferences.StreamResponses), selectCmd: cmdTogglePref("stream_responses")},
		{title: "Show Tool Activity", data: boolStatus(appConfig.Preferences.ShowToolActivity), selectCmd: cmdTogglePref("show_tool_activity")},
		{title: "Auto-copy Code Blocks", data: boolStatus(appConfig.Preferences.AutoCopyCode), selectCmd: cmdTogglePref("auto_copy_code")},
		{title: "Data & Privacy", selectCmd: cmdSetMenu(dataPrivacyMenu)},
		{title: "← Back", selectCmd: cmdBack()},
	}
	return defaultList("Settings", items)
}

func dataPrivacyMenu(appConfig AppConfig) list.Model {
	dataDir, _ := FullFilePath(".shell-ai")
	items := []menuItem{
		{title: "Data Directory", data: dataDir},
		{title: "Clear Conversation History", selectCmd: cmdSetMenu(clearHistoryConfirmMenu)},
		{title: "Clear Knowledge Graph", selectCmd: cmdSetMenu(clearKnowledgeConfirmMenu)},
		{title: "Clear Documentation Cache", selectCmd: cmdSetMenu(clearDocsConfirmMenu)},
		{title: "Clear All Data", data: "nuclear option", selectCmd: cmdSetMenu(clearAllDataConfirmMenu)},
		{title: "← Back", selectCmd: cmdBack()},
	}
	return defaultList("Data & Privacy", items)
}

func clearHistoryConfirmMenu(appConfig AppConfig) list.Model {
	items := []menuItem{{title: "Yes, clear history", selectCmd: clearDataAction("history")}, {title: "No, cancel", selectCmd: cmdBack()}}
	return defaultList("Clear all conversation history?", items)
}

func clearKnowledgeConfirmMenu(appConfig AppConfig) list.Model {
	items := []menuItem{{title: "Yes, clear knowledge", selectCmd: clearDataAction("knowledge")}, {title: "No, cancel", selectCmd: cmdBack()}}
	return defaultList("Clear knowledge graph?", items)
}

func clearDocsConfirmMenu(appConfig AppConfig) list.Model {
	items := []menuItem{{title: "Yes, clear docs cache", selectCmd: clearDataAction("docs")}, {title: "No, cancel", selectCmd: cmdBack()}}
	return defaultList("Clear documentation cache?", items)
}

func clearAllDataConfirmMenu(appConfig AppConfig) list.Model {
	items := []menuItem{{title: "Yes, DELETE ALL DATA", data: "cannot undo", selectCmd: clearDataAction("all")}, {title: "No, cancel", selectCmd: cmdBack()}}
	return defaultList("Delete ALL shell-ai data?", items)
}

func clearDataAction(dataType string) tea.Cmd {
	return func() tea.Msg {
		dataDir, _ := FullFilePath(".shell-ai")
		dbPath := dataDir + "/memory.db"
		switch dataType {
		case "all":
			os.RemoveAll(dataDir)
			os.MkdirAll(dataDir, 0700)
		case "history", "knowledge", "docs":
			os.Remove(dbPath)
		}
		return backMsg{}
	}
}

func resetConfirmMenu(appConfig AppConfig) list.Model {
	items := []menuItem{{title: "Yes, reset config to defaults", selectCmd: resetConfigAction()}, {title: "No, cancel", selectCmd: cmdBack()}}
	return defaultList("Reset configuration to defaults?", items)
}

func resetConfigAction() tea.Cmd {
	return func() tea.Msg {
		ResetAppConfigToDefault()
		return quitMsg{}
	}
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func PrintConfigErrorMessage(err error) {
	maxWidth := util.GetTermSafeMaxWidth()
	styleRed := lipgloss.NewStyle().Foreground(lipgloss.Color("9")).PaddingLeft(2)
	styleDim := lipgloss.NewStyle().Faint(true).Width(maxWidth).PaddingLeft(2)

	r, _ := glamour.NewTermRenderer(glamour.WithAutoStyle())

	msg1 := styleRed.Render("Failed to load config file.")
	filePath, _ := FullFilePath(configFilePath)
	msg2 := styleDim.Render(err.Error())

	messageString := fmt.Sprintf(
		"---\n"+
			"# Options:\n\n"+
			"1. Run `q config revert` to load the automatic backup.\n"+
			"2. Run `q config reset` to reset to defaults.\n"+
			"3. Fix manually at: `%s`\n\n",
		filePath)

	msg3, _ := r.Render(messageString)
	fmt.Printf("\n%s\n\n%s%s", msg1, msg2, msg3)
}

func handleConfigResets(args []string) {
	if len(args) < 2 {
		return
	}
	greyStylePadded := greyStyle.PaddingLeft(2)
	reader := bufio.NewReader(os.Stdin)
	warningMessage, confirmationMessage := getMessages(args[1], greyStylePadded)
	fmt.Print("\n" + styleRed.PaddingLeft(2).Render(warningMessage) + "\n\n" + confirmationMessage + " ")
	response, _ := reader.ReadString('\n')
	response = strings.ToLower(strings.TrimSpace(response))
	if response == "yes" || response == "y" {
		handleResetOrRevert(args[1])
	} else {
		fmt.Println("\n" + styleRed.PaddingLeft(2).Render("Operation cancelled.\n"))
	}
	os.Exit(0)
}

func getMessages(arg string, greyStylePadded lipgloss.Style) (string, string) {
	warningMessage := "WARNING: You are about to "
	confirmationMessage := greyStylePadded.Render("Do you want to continue? (y/N):")
	switch arg {
	case "reset":
		warningMessage += "reset the config file to the default."
	case "revert":
		warningMessage += "revert the config file to the last working automatic backup."
	}
	return warningMessage, confirmationMessage
}

func handleResetOrRevert(arg string) {
	var err error
	var message string
	switch arg {
	case "reset":
		err = ResetAppConfigToDefault()
		message = "Config reset to default.\n"
	case "revert":
		err = RevertAppConfigToBackup()
		message = "Config reverted to backup.\n"
	}
	if err == nil {
		fmt.Println("\n" + greyStyle.PaddingLeft(2).Render(message))
	} else {
		fmt.Println("\n" + styleRed.PaddingLeft(2).Render("Operation failed.\n"))
		fmt.Println("\n" + styleRed.PaddingLeft(2).Render(fmt.Sprintf("Error: %s\n", err)))
	}
}

func RunConfigProgram(args []string) {
	handleConfigResets(args)
	appConfig, err := LoadAppConfig()
	if err != nil {
		PrintConfigErrorMessage(err)
		os.Exit(1)
	}
	m := model{
		appConfig: appConfig,
		list:      mainMenu(appConfig),
		state:     state{page: ListPage, menu: mainMenu},
	}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}
