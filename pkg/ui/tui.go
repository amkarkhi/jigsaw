package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"

	jigsawctx "github.com/amkarkhi/jigsaw/pkg/context"
	"github.com/amkarkhi/jigsaw/pkg/engine"
	"github.com/amkarkhi/jigsaw/pkg/types"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rs/zerolog"
)

// TUI provides a terminal user interface for Jigsaw — browse the config
// and (on the runnable tabs) press 'r' to execute the selected flow/task/
// logic against the live engine. Inputs are edited in $EDITOR; results
// render in a per-task trace view (esc returns to browse).
type TUI struct {
	config        *types.Config
	engine        *engine.Engine
	logger        zerolog.Logger
	logicRegistry []string

	// Stable, sorted name lists so the cursor maps to the same item across
	// renders. Maps have non-deterministic iteration order, which would
	// otherwise make 'r' run a different thing than the user pointed at.
	flowNames     []string
	taskNames     []string
	providerNames []string
	endpointNames []string

	width     int
	height    int
	activeTab int
	cursor    int
	tabs      []string

	// View mode: "browse" shows the tab list; "results" shows the last run's
	// trace. Esc from "results" returns to "browse".
	mode       string
	scroll     int
	lastResult *runResult
	lastErr    string
	status     string // transient status line (e.g. "Running...")
}

// runResult captures one playground-style execution for rendering.
type runResult struct {
	kind   string // "flow" | "task" | "logic"
	target string
	exec   *types.FlowExecution
	result map[string]any
	runErr error
}

// editorDoneMsg is delivered when the user closes $EDITOR.
type editorDoneMsg struct {
	path string
	kind string // "flow" | "task" | "logic"
	name string
	err  error
}

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#7D56F4")).
			MarginLeft(2)

	tabStyle = lipgloss.NewStyle().
			Padding(0, 2).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#874BFD"))

	activeTabStyle = tabStyle.Copy().
			BorderForeground(lipgloss.Color("#F780E2")).
			Bold(true)

	listStyle = lipgloss.NewStyle().
			MarginLeft(4).
			MarginTop(1)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F780E2")).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#626262")).
			MarginTop(1).
			MarginLeft(2)

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FF5C8A")).
			Bold(true)

	okStyle = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#5EE2A0")).
		Bold(true)

	dimStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#8A8A8A"))
)

// NewTUI creates a new TUI instance. The engine is required so the TUI
// can execute selected flows/tasks/logic against the live registry.
func NewTUI(config *types.Config, eng *engine.Engine, logger zerolog.Logger) *TUI {
	return &TUI{
		config:        config,
		engine:        eng,
		logger:        logger,
		logicRegistry: eng.ListLogicHandlers(),
		flowNames:     sortedKeys(config.Flows),
		taskNames:     sortedKeys(config.Tasks),
		providerNames: sortedKeys(config.Providers),
		endpointNames: sortedKeys(config.Endpoints),
		tabs:          []string{"Flows", "Tasks", "Providers", "Endpoints", "Logic Registry", "Overview"},
		mode:          "browse",
	}
}

func (m *TUI) Init() tea.Cmd {
	return nil
}

func (m *TUI) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case editorDoneMsg:
		return m, m.afterEditor(msg)

	case tea.KeyMsg:
		// Results mode has a different keymap (scrolling + esc to exit).
		if m.mode == "results" {
			return m.updateResults(msg)
		}
		return m.updateBrowse(msg)
	}

	return m, nil
}

func (m *TUI) updateBrowse(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit

	case "tab", "right":
		m.activeTab = (m.activeTab + 1) % len(m.tabs)
		m.cursor = 0
		m.status = ""
		return m, nil

	case "shift+tab", "left":
		m.activeTab = (m.activeTab - 1 + len(m.tabs)) % len(m.tabs)
		m.cursor = 0
		m.status = ""
		return m, nil

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil

	case "down", "j":
		max := m.getMaxCursor()
		if m.cursor < max {
			m.cursor++
		}
		return m, nil

	case "r":
		return m, m.launchRun()
	}
	return m, nil
}

func (m *TUI) updateResults(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc":
		m.mode = "browse"
		m.scroll = 0
		return m, nil
	case "up", "k":
		if m.scroll > 0 {
			m.scroll--
		}
		return m, nil
	case "down", "j":
		m.scroll++
		return m, nil
	case "g":
		m.scroll = 0
		return m, nil
	}
	return m, nil
}

// launchRun figures out what's selected on the current tab, builds an
// inputs template, opens $EDITOR on it, and dispatches back to
// afterEditor when the editor exits.
func (m *TUI) launchRun() tea.Cmd {
	kind, name := m.runTarget()
	if kind == "" {
		m.status = "Nothing to run on this tab — switch to Flows, Tasks, or Logic Registry."
		return nil
	}

	tmpl := map[string]any{
		"inputs":  map[string]any{},
		"headers": map[string]any{},
		"sub":     0,
	}
	if kind != "flow" {
		tmpl["params"] = map[string]any{}
	}
	buf, _ := json.MarshalIndent(tmpl, "", "  ")
	preamble := fmt.Sprintf("// Run %s: %s\n// Edit this JSON, then save & close to execute. Fields:\n//   inputs  — initial inputs map\n//   headers — request headers map\n//   sub     — endpoint sub variant (int)\n//   params  — task/logic param overrides (task/logic runs only)\n// Comments starting with // are stripped before parsing.\n", kind, name)
	body := append([]byte(preamble), buf...)

	tmp, err := os.CreateTemp("", "jigsaw-run-*.json")
	if err != nil {
		m.status = "Failed to create temp file: " + err.Error()
		return nil
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		m.status = "Failed to write template: " + err.Error()
		return nil
	}
	_ = tmp.Close()

	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}

	// $EDITOR may be a command string with args (e.g. "code --wait"). Split.
	parts := strings.Fields(editor)
	parts = append(parts, tmp.Name())
	c := exec.Command(parts[0], parts[1:]...)

	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorDoneMsg{path: tmp.Name(), kind: kind, name: name, err: err}
	})
}

// runTarget returns the kind+name of the currently selected runnable, or
// ("", "") if the current tab isn't runnable (or has nothing selected).
func (m *TUI) runTarget() (string, string) {
	switch m.activeTab {
	case 0: // Flows
		if m.cursor < len(m.flowNames) {
			return "flow", m.flowNames[m.cursor]
		}
	case 1: // Tasks
		if m.cursor < len(m.taskNames) {
			return "task", m.taskNames[m.cursor]
		}
	case 4: // Logic Registry
		if m.cursor < len(m.logicRegistry) {
			return "logic", m.logicRegistry[m.cursor]
		}
	}
	return "", ""
}

func (m *TUI) afterEditor(msg editorDoneMsg) tea.Cmd {
	defer os.Remove(msg.path)

	if msg.err != nil {
		m.status = "Editor exited with error: " + msg.err.Error()
		return nil
	}

	raw, err := os.ReadFile(msg.path)
	if err != nil {
		m.status = "Read temp file: " + err.Error()
		return nil
	}
	cleaned := stripLineComments(raw)

	var body struct {
		Inputs  map[string]any    `json:"inputs"`
		Headers map[string]string `json:"headers"`
		Params  map[string]any    `json:"params"`
		Sub     int               `json:"sub"`
	}
	if err := json.Unmarshal(cleaned, &body); err != nil {
		m.status = "Invalid JSON in editor: " + err.Error()
		return nil
	}

	m.status = "Running..."
	res := m.execute(msg.kind, msg.name, body.Inputs, body.Headers, body.Params, body.Sub)
	m.lastResult = res
	m.mode = "results"
	m.scroll = 0
	m.status = ""
	return nil
}

// execute runs the selected target through the engine using the same
// approach as the dashboard playground (synthetic flow for task/logic
// runs, stub provider registry so we don't need real I/O wired up).
func (m *TUI) execute(kind, name string, inputs map[string]any, headers map[string]string, params map[string]any, sub int) *runResult {
	res := &runResult{kind: kind, target: name}

	cfg := m.config
	var flow *types.Flow

	switch kind {
	case "flow":
		f, ok := cfg.Flows[name]
		if !ok {
			res.runErr = fmt.Errorf("flow %q not found", name)
			return res
		}
		flow = f
	case "task":
		if _, ok := cfg.Tasks[name]; !ok {
			res.runErr = fmt.Errorf("task %q not found", name)
			return res
		}
		flow = &types.Flow{
			Name:        "__tui_task__",
			Description: "ad-hoc single-task run",
			Tasks:       []types.TaskRef{{Name: name, Params: params}},
		}
	case "logic":
		// Clone config so the injected synthetic task can't leak into the
		// live engine config (shared with any other running flows).
		clone := *cfg
		clone.Tasks = make(map[string]*types.Task, len(cfg.Tasks)+1)
		maps.Copy(clone.Tasks, cfg.Tasks)
		const synth = "__tui_logic__"
		clone.Tasks[synth] = &types.Task{
			Name:        synth,
			Description: "synthetic task for logic " + name,
			Logic:       name,
			Params:      params,
		}
		cfg = &clone
		flow = &types.Flow{
			Name:        "__tui_logic__",
			Description: "ad-hoc single-logic run",
			Tasks:       []types.TaskRef{{Name: synth}},
		}
	default:
		res.runErr = fmt.Errorf("unknown run kind %q", kind)
		return res
	}

	execCtx := jigsawctx.New(context.Background(), flow.Name, sub, inputs, headers)
	execCtx.TraceEnabled = true
	execCtx = jigsawctx.WithProviders(execCtx, newStubProviderRegistry(cfg))
	execCtx = jigsawctx.WithLogger(execCtx, m.logger)
	execCtx.Engine = m.engine

	exec := m.engine.FlowExecutorFor(cfg)
	flowExec, err := exec.Execute(execCtx, flow)
	res.exec = flowExec
	res.runErr = err
	if flowExec != nil && flowExec.Context != nil {
		res.result = collectResult(flowExec)
	}
	return res
}

func (m *TUI) View() string {
	if m.width == 0 {
		return "Loading..."
	}
	if m.mode == "results" {
		return m.renderResults()
	}

	var b strings.Builder

	// Title
	b.WriteString(titleStyle.Render("🧩 Jigsaw Configuration Manager"))
	b.WriteString("\n\n")

	// Tabs
	var tabs []string
	for i, tab := range m.tabs {
		style := tabStyle
		if i == m.activeTab {
			style = activeTabStyle
		}
		tabs = append(tabs, style.Render(tab))
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, tabs...))
	b.WriteString("\n\n")

	// Content based on active tab
	switch m.activeTab {
	case 0:
		b.WriteString(m.renderFlows())
	case 1:
		b.WriteString(m.renderTasks())
	case 2:
		b.WriteString(m.renderProviders())
	case 3:
		b.WriteString(m.renderEndpoints())
	case 4:
		b.WriteString(m.renderLogicRegistry())
	case 5:
		b.WriteString(m.renderOverview())
	}

	// Status line (transient — errors from editor/parse land here).
	if m.status != "" {
		b.WriteString("\n")
		b.WriteString(helpStyle.Render(m.status))
	}

	// Help — show 'r' hint only on runnable tabs.
	b.WriteString("\n")
	help := "Tab/Shift+Tab: Switch tabs • ↑/↓: Navigate • q: Quit"
	if m.activeTab == 0 || m.activeTab == 1 || m.activeTab == 4 {
		help = "Tab/Shift+Tab: Switch tabs • ↑/↓: Navigate • r: Run in $EDITOR • q: Quit"
	}
	b.WriteString(helpStyle.Render(help))

	return b.String()
}

func (m *TUI) renderFlows() string {
	var b strings.Builder
	b.WriteString(listStyle.Render(fmt.Sprintf("📋 Flows (%d total)", len(m.flowNames))))
	b.WriteString("\n\n")

	for i, name := range m.flowNames {
		flow := m.config.Flows[name]
		prefix := "  "
		if i == m.cursor {
			prefix = "▶ "
		}
		style := lipgloss.NewStyle()
		if i == m.cursor {
			style = selectedStyle
		}
		b.WriteString(style.Render(fmt.Sprintf("%s%s", prefix, name)))
		b.WriteString("\n")
		if i == m.cursor {
			b.WriteString(fmt.Sprintf("    Description: %s\n", flow.Description))
			b.WriteString(fmt.Sprintf("    Tasks: %d\n", len(flow.Tasks)))
			if parallels := countParallelBlocks(flow.Tasks); parallels > 0 {
				b.WriteString(fmt.Sprintf("    Parallel blocks: %d\n", parallels))
			}
			if flow.Inherits != "" {
				b.WriteString(fmt.Sprintf("    Inherits: %s\n", flow.Inherits))
			}
		}
	}
	return b.String()
}

func (m *TUI) renderTasks() string {
	var b strings.Builder
	b.WriteString(listStyle.Render(fmt.Sprintf("⚙️  Tasks (%d total)", len(m.taskNames))))
	b.WriteString("\n\n")

	for i, name := range m.taskNames {
		task := m.config.Tasks[name]
		prefix := "  "
		if i == m.cursor {
			prefix = "▶ "
		}
		style := lipgloss.NewStyle()
		if i == m.cursor {
			style = selectedStyle
		}
		b.WriteString(style.Render(fmt.Sprintf("%s%s", prefix, name)))
		b.WriteString("\n")
		if i == m.cursor {
			b.WriteString(fmt.Sprintf("    Description: %s\n", task.Description))
			b.WriteString(fmt.Sprintf("    Logic: %s\n", task.Logic))
			if task.Provider != "" {
				b.WriteString(fmt.Sprintf("    Provider: %s\n", task.Provider))
			}
			b.WriteString(fmt.Sprintf("    Params: %d\n", len(task.Params)))
			if task.Inherits != "" {
				b.WriteString(fmt.Sprintf("    Inherits: %s\n", task.Inherits))
			}
		}
	}
	return b.String()
}

func (m *TUI) renderProviders() string {
	var b strings.Builder
	b.WriteString(listStyle.Render(fmt.Sprintf("🔌 Providers (%d total)", len(m.providerNames))))
	b.WriteString("\n\n")

	for i, name := range m.providerNames {
		provider := m.config.Providers[name]
		prefix := "  "
		if i == m.cursor {
			prefix = "▶ "
		}
		style := lipgloss.NewStyle()
		if i == m.cursor {
			style = selectedStyle
		}
		b.WriteString(style.Render(fmt.Sprintf("%s%s", prefix, name)))
		b.WriteString("\n")
		if i == m.cursor {
			b.WriteString(fmt.Sprintf("    Type: %s\n", provider.Type))
			b.WriteString(fmt.Sprintf("    Init Mode: %s\n", provider.InitMode))
			if provider.PoolSize > 0 {
				b.WriteString(fmt.Sprintf("    Pool Size: %d\n", provider.PoolSize))
			}
		}
	}
	return b.String()
}

func (m *TUI) renderEndpoints() string {
	var b strings.Builder
	b.WriteString(listStyle.Render(fmt.Sprintf("🌐 Endpoints (%d total)", len(m.endpointNames))))
	b.WriteString("\n\n")

	for i, name := range m.endpointNames {
		endpoint := m.config.Endpoints[name]
		prefix := "  "
		if i == m.cursor {
			prefix = "▶ "
		}
		style := lipgloss.NewStyle()
		if i == m.cursor {
			style = selectedStyle
		}
		b.WriteString(style.Render(fmt.Sprintf("%s%s", prefix, name)))
		b.WriteString("\n")
		if i == m.cursor {
			b.WriteString(fmt.Sprintf("    Path: %s %s\n", endpoint.Method, endpoint.Path))
			b.WriteString(fmt.Sprintf("    Description: %s\n", endpoint.Description))
			b.WriteString("    Flow Mappings:\n")
			for _, mapping := range endpoint.Flows {
				b.WriteString(fmt.Sprintf("      sub=%d → %s\n", mapping.Sub, mapping.FlowName))
			}
		}
	}
	return b.String()
}

func (m *TUI) renderLogicRegistry() string {
	var b strings.Builder
	b.WriteString(listStyle.Render(fmt.Sprintf("🔧 Registered Logic Handlers (%d total)", len(m.logicRegistry))))
	b.WriteString("\n\n")

	if len(m.logicRegistry) == 0 {
		b.WriteString("    No logic handlers registered yet.\n")
		b.WriteString("    Register handlers in your application code:\n")
		b.WriteString("    eng.MustRegisterLogic(\"handler_name\", yourFunction)\n")
		return b.String()
	}

	for i, name := range m.logicRegistry {
		prefix := "  "
		if i == m.cursor {
			prefix = "▶ "
		}
		style := lipgloss.NewStyle()
		if i == m.cursor {
			style = selectedStyle
		}
		b.WriteString(style.Render(fmt.Sprintf("%s✓ %s", prefix, name)))
		b.WriteString("\n")
	}
	return b.String()
}

func (m *TUI) renderOverview() string {
	var b strings.Builder
	b.WriteString(listStyle.Render("📊 Configuration Overview"))
	b.WriteString("\n\n")

	stats := []struct {
		label string
		count int
		icon  string
	}{
		{"Flows", len(m.config.Flows), "📋"},
		{"Tasks", len(m.config.Tasks), "⚙️"},
		{"Providers", len(m.config.Providers), "🔌"},
		{"Endpoints", len(m.config.Endpoints), "🌐"},
		{"Logic Handlers", len(m.logicRegistry), "🔧"},
	}

	for _, stat := range stats {
		b.WriteString(fmt.Sprintf("    %s %s: %d\n", stat.icon, stat.label, stat.count))
	}

	b.WriteString("\n  Configuration Status:\n")
	if len(m.config.Flows) == 0 {
		b.WriteString("    ⚠️  No flows configured\n")
	} else {
		b.WriteString("    ✓ Flows configured\n")
	}
	if len(m.config.Tasks) == 0 {
		b.WriteString("    ⚠️  No tasks configured\n")
	} else {
		b.WriteString("    ✓ Tasks configured\n")
	}
	if len(m.logicRegistry) == 0 {
		b.WriteString("    ⚠️  No logic handlers registered\n")
	} else {
		b.WriteString(fmt.Sprintf("    ✓ %d logic handlers registered\n", len(m.logicRegistry)))
	}

	b.WriteString("\n  Logic Implementation Status:\n")
	unimplemented := 0
	for _, task := range m.config.Tasks {
		found := slices.Contains(m.logicRegistry, task.Logic)
		if !found {
			unimplemented++
		}
	}
	if unimplemented > 0 {
		b.WriteString(fmt.Sprintf("    ⚠️  %d tasks have unimplemented logic handlers\n", unimplemented))
	} else if len(m.config.Tasks) > 0 {
		b.WriteString("    ✓ All tasks have registered logic handlers\n")
	}

	return b.String()
}

// renderResults shows the trace view for the last execution. Scrollable
// via j/k or ↑/↓; esc returns to browse.
func (m *TUI) renderResults() string {
	if m.lastResult == nil {
		return "No result. Press esc to return."
	}
	r := m.lastResult

	var b strings.Builder
	title := fmt.Sprintf("▶ Run %s: %s", r.kind, r.target)
	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n\n")

	// Overall status line
	if r.runErr != nil {
		b.WriteString("  ")
		b.WriteString(errorStyle.Render("✘ FAILED"))
		b.WriteString(": " + r.runErr.Error() + "\n")
	} else if r.exec != nil {
		b.WriteString("  ")
		b.WriteString(okStyle.Render("✓ " + string(r.exec.Status)))
		if r.exec.Context != nil && r.exec.Context.RequestID != "" {
			b.WriteString(dimStyle.Render(fmt.Sprintf("  (request_id=%s)", r.exec.Context.RequestID)))
		}
		b.WriteString("\n")
	}

	// Per-task traces
	if r.exec != nil && len(r.exec.Tasks) > 0 {
		traces := append([]*types.TaskExecution(nil), r.exec.Tasks...)
		sort.SliceStable(traces, func(i, j int) bool { return traces[i].StartedAt.Before(traces[j].StartedAt) })

		b.WriteString("\n  Tasks:\n")
		for _, te := range traces {
			name := ""
			if te.Task != nil {
				name = te.Task.Name
			}
			statusStr := string(te.Status)
			dur := ""
			if te.CompletedAt != nil {
				dur = fmt.Sprintf(" %dms", te.CompletedAt.Sub(te.StartedAt).Milliseconds())
			}
			marker := okStyle.Render("✓")
			if te.Error != nil {
				marker = errorStyle.Render("✘")
			} else if te.Skipped {
				marker = dimStyle.Render("○")
			}
			b.WriteString(fmt.Sprintf("    %s %s [%s%s]\n", marker, name, statusStr, dur))
			if te.Task != nil && te.Task.Logic != "" {
				b.WriteString(dimStyle.Render(fmt.Sprintf("        logic: %s\n", te.Task.Logic)))
			}
			if te.FallbackUsed {
				b.WriteString(dimStyle.Render("        fallback used (logic not registered — echoed inputs)\n"))
			}
			if len(te.Inputs) > 0 {
				b.WriteString("        inputs:  " + compactJSON(te.Inputs) + "\n")
			}
			if len(te.Params) > 0 {
				b.WriteString("        params:  " + compactJSON(te.Params) + "\n")
			}
			if len(te.Outputs) > 0 {
				b.WriteString("        outputs: " + compactJSON(te.Outputs) + "\n")
			}
			if te.Error != nil {
				b.WriteString("        " + errorStyle.Render("error: "+te.Error.Error()) + "\n")
			}
			if len(te.Annotations) > 0 {
				b.WriteString(dimStyle.Render("        annotations: "+compactJSON(te.Annotations)) + "\n")
			}
		}
	}

	// Final result body
	if r.result != nil && r.runErr == nil {
		b.WriteString("\n  Result:\n")
		pretty, _ := json.MarshalIndent(r.result, "    ", "  ")
		b.WriteString("    " + string(pretty) + "\n")
	}

	// Apply vertical scroll by trimming top lines.
	full := b.String()
	lines := strings.Split(full, "\n")
	if m.scroll > 0 && m.scroll < len(lines) {
		lines = lines[m.scroll:]
	}
	body := strings.Join(lines, "\n")

	return body + "\n" + helpStyle.Render("↑/↓: Scroll • g: Top • esc: Back • q: Quit")
}

func (m *TUI) getMaxCursor() int {
	switch m.activeTab {
	case 0:
		return len(m.flowNames) - 1
	case 1:
		return len(m.taskNames) - 1
	case 2:
		return len(m.providerNames) - 1
	case 3:
		return len(m.endpointNames) - 1
	case 4:
		return len(m.logicRegistry) - 1
	default:
		return 0
	}
}

// countParallelBlocks reports how many parallel blocks appear anywhere in
// the task tree (recursing into branches). Cheap, used for flow summaries.
func countParallelBlocks(tasks []types.TaskRef) int {
	count := 0
	for _, t := range tasks {
		if t.Parallel == nil {
			continue
		}
		count++
		for _, br := range t.Parallel.Branches {
			count += countParallelBlocks(br.Tasks)
		}
	}
	return count
}

// sortedKeys returns the keys of any string-keyed map in lexicographic
// order. Used so the cursor maps to a stable item across renders.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// stripLineComments removes `//`-prefixed lines so the editor template
// can include human-readable hints without breaking json.Unmarshal.
func stripLineComments(raw []byte) []byte {
	var out []byte
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		out = append(out, []byte(line)...)
		out = append(out, '\n')
	}
	return out
}

// compactJSON marshals v to a single-line JSON string for trace display.
// Errors fall back to Go's default %v so we never blank-out a row.
func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

// collectResult mirrors the playground's narrowing: when the flow ends in
// a plain task, surface that task's outputs. Otherwise fall back to a
// snapshot of the full scope so the user always sees *something*.
func collectResult(flowExec *types.FlowExecution) map[string]any {
	if flowExec == nil || flowExec.Context == nil {
		return nil
	}
	if flow := flowExec.Flow; flow != nil {
		for i := len(flow.Tasks) - 1; i >= 0; i-- {
			name := flow.Tasks[i].Name
			if name == "" {
				continue
			}
			for _, te := range flowExec.Tasks {
				if te.Task != nil && te.Task.Name == name && te.Outputs != nil {
					return te.Outputs
				}
			}
			break
		}
	}
	snap := make(map[string]any, len(flowExec.Context.Scope))
	for k, sv := range flowExec.Context.Scope {
		snap[k] = sv.Value
	}
	return snap
}

// ---- stub providers -------------------------------------------------------
//
// Mirrors pkg/dashboard's playground stub: satisfies types.ProviderRegistry
// with no-op everything so TUI runs don't require real provider I/O wired
// up. Tasks that legitimately need a real provider will surface clear
// errors from their logic handlers (or hit the unimplemented fallback).

type stubProviderRegistry struct{ cfg *types.Config }

func newStubProviderRegistry(cfg *types.Config) *stubProviderRegistry {
	return &stubProviderRegistry{cfg: cfg}
}

func (s *stubProviderRegistry) Get(name string) (types.ProviderInstance, error) {
	p, ok := s.cfg.Providers[name]
	if !ok {
		p = &types.Provider{Name: name, Type: "stub"}
	}
	return &stubProvider{provider: p, connected: true}, nil
}
func (s *stubProviderRegistry) Register(_ string, _ types.ProviderInstance) error { return nil }
func (s *stubProviderRegistry) Close() error                                      { return nil }

type stubProvider struct {
	provider  *types.Provider
	connected bool
}

func (s *stubProvider) Connect(_ context.Context) error    { s.connected = true; return nil }
func (s *stubProvider) Disconnect(_ context.Context) error { s.connected = false; return nil }
func (s *stubProvider) IsConnected() bool                  { return s.connected }
func (s *stubProvider) GetConnection() any                 { return nil }
func (s *stubProvider) GetProvider() *types.Provider       { return s.provider }
