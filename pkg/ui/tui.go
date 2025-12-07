package ui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/amkarkhi/jigsaw/pkg/types"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// TUI provides a terminal user interface for Jigsaw
type TUI struct {
	config        *types.Config
	logicRegistry []string
	width         int
	height        int
	activeTab     int
	cursor        int
	tabs          []string
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
)

// NewTUI creates a new TUI instance
func NewTUI(config *types.Config, logicRegistry []string) *TUI {
	return &TUI{
		config:        config,
		logicRegistry: logicRegistry,
		tabs:          []string{"Flows", "Tasks", "Providers", "Endpoints", "Logic Registry", "Overview"},
		activeTab:     0,
		cursor:        0,
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

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit

		case "tab", "right":
			m.activeTab = (m.activeTab + 1) % len(m.tabs)
			m.cursor = 0
			return m, nil

		case "shift+tab", "left":
			m.activeTab = (m.activeTab - 1 + len(m.tabs)) % len(m.tabs)
			m.cursor = 0
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
		}
	}

	return m, nil
}

func (m *TUI) View() string {
	if m.width == 0 {
		return "Loading..."
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

	// Help
	b.WriteString("\n")
	b.WriteString(helpStyle.Render("Tab/Shift+Tab: Switch tabs • ↑/↓: Navigate • q: Quit"))

	return b.String()
}

func (m *TUI) renderFlows() string {
	var b strings.Builder
	b.WriteString(listStyle.Render(fmt.Sprintf("📋 Flows (%d total)", len(m.config.Flows))))
	b.WriteString("\n\n")

	i := 0
	for name, flow := range m.config.Flows {
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
			if flow.Inherits != "" {
				b.WriteString(fmt.Sprintf("    Inherits: %s\n", flow.Inherits))
			}
		}

		i++
	}

	return b.String()
}

func (m *TUI) renderTasks() string {
	var b strings.Builder
	b.WriteString(listStyle.Render(fmt.Sprintf("⚙️  Tasks (%d total)", len(m.config.Tasks))))
	b.WriteString("\n\n")

	i := 0
	for name, task := range m.config.Tasks {
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
			b.WriteString(fmt.Sprintf("    Inputs: %d, Outputs: %d\n", len(task.Inputs), len(task.Outputs)))
			if task.Inherits != "" {
				b.WriteString(fmt.Sprintf("    Inherits: %s\n", task.Inherits))
			}
		}

		i++
	}

	return b.String()
}

func (m *TUI) renderProviders() string {
	var b strings.Builder
	b.WriteString(listStyle.Render(fmt.Sprintf("🔌 Providers (%d total)", len(m.config.Providers))))
	b.WriteString("\n\n")

	i := 0
	for name, provider := range m.config.Providers {
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

		i++
	}

	return b.String()
}

func (m *TUI) renderEndpoints() string {
	var b strings.Builder
	b.WriteString(listStyle.Render(fmt.Sprintf("🌐 Endpoints (%d total)", len(m.config.Endpoints))))
	b.WriteString("\n\n")

	i := 0
	for name, endpoint := range m.config.Endpoints {
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
			b.WriteString(fmt.Sprintf("    Flow Mappings: %s\n", ""))
			for _, mapping := range endpoint.Flows {
				b.WriteString(fmt.Sprintf("      sub=%d → %s\n", mapping.Sub, mapping.FlowName))
			}
		}

		i++
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

		status := "✓"
		b.WriteString(style.Render(fmt.Sprintf("%s%s %s", prefix, status, name)))
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

	b.WriteString("\n")
	b.WriteString("  Configuration Status:\n")

	// Check for common issues
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

	// Check for unimplemented logic
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

func (m *TUI) getMaxCursor() int {
	switch m.activeTab {
	case 0:
		return len(m.config.Flows) - 1
	case 1:
		return len(m.config.Tasks) - 1
	case 2:
		return len(m.config.Providers) - 1
	case 3:
		return len(m.config.Endpoints) - 1
	case 4:
		return len(m.logicRegistry) - 1
	default:
		return 0
	}
}
