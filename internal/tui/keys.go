package tui

import "github.com/charmbracelet/bubbles/key"

// KeyMap key bindings
type KeyMap struct {
	SplitView      key.Binding
	DashboardView  key.Binding
	TaskListView   key.Binding
	ToggleSidebar  key.Binding
	ToggleProvider key.Binding
	Tab            key.Binding
	ShiftTab       key.Binding
	Up             key.Binding
	Down           key.Binding
	Left           key.Binding
	Right          key.Binding
	Enter          key.Binding
	Escape         key.Binding
	Slash          key.Binding
	Help           key.Binding
	Quit           key.Binding
	PageUp         key.Binding
	PageDown       key.Binding
	Home           key.Binding
	End            key.Binding
	Settings       key.Binding
}

// DefaultKeyMap default key bindings
var DefaultKeyMap = KeyMap{
	SplitView: key.NewBinding(
		key.WithKeys("alt+shift+1"),
		key.WithHelp("Alt+Shift+1", "Split view"),
	),
	DashboardView: key.NewBinding(
		key.WithKeys("alt+shift+2"),
		key.WithHelp("Alt+Shift+2", "Dashboard"),
	),
	TaskListView: key.NewBinding(
		key.WithKeys("alt+shift+3"),
		key.WithHelp("Alt+Shift+3", "Task list"),
	),
	ToggleSidebar: key.NewBinding(
		key.WithKeys("`"),
		key.WithHelp("`", "Sidebar"),
	),
	ToggleProvider: key.NewBinding(
		key.WithKeys("$"),
		key.WithHelp("$", "Change provider"),
	),
	Tab: key.NewBinding(
		key.WithKeys("tab"),
		key.WithHelp("Tab", "Switch view"),
	),
	ShiftTab: key.NewBinding(
		key.WithKeys("shift+tab"),
		key.WithHelp("Shift+Tab", "Previous view"),
	),
	Up: key.NewBinding(
		key.WithKeys("up"),
		key.WithHelp("↑", "Up"),
	),
	Down: key.NewBinding(
		key.WithKeys("down"),
		key.WithHelp("↓", "Down"),
	),
	Left: key.NewBinding(
		key.WithKeys("left"),
		key.WithHelp("←", "Left"),
	),
	Right: key.NewBinding(
		key.WithKeys("right"),
		key.WithHelp("→", "Right"),
	),
	Enter: key.NewBinding(
		key.WithKeys("enter"),
		key.WithHelp("Enter", "Execute"),
	),
	Escape: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("Esc", "Cancel"),
	),
	Slash: key.NewBinding(
		key.WithKeys("/"),
		key.WithHelp("/", "Input"),
	),
	Help: key.NewBinding(
		key.WithKeys("?"),
		key.WithHelp("?", "Help"),
	),
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "Quit"),
	),
	PageUp: key.NewBinding(
		key.WithKeys("pgup"),
		key.WithHelp("PgUp", "Page up"),
	),
	PageDown: key.NewBinding(
		key.WithKeys("pgdown"),
		key.WithHelp("PgDn", "Page down"),
	),
	Home: key.NewBinding(
		key.WithKeys("home"),
		key.WithHelp("Home", "Top"),
	),
	End: key.NewBinding(
		key.WithKeys("end"),
		key.WithHelp("End", "Bottom"),
	),
	Settings: key.NewBinding(
		key.WithKeys("f10"),
		key.WithHelp("F10", "Settings"),
	),
}

// ShortHelp short help
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.SplitView, k.DashboardView, k.TaskListView, k.Tab, k.Enter, k.Settings, k.Quit}
}

// FullHelp full help
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.SplitView, k.DashboardView, k.TaskListView},
		{k.Tab, k.ShiftTab},
		{k.Up, k.Down, k.Left, k.Right},
		{k.Enter, k.Escape, k.Slash},
		{k.PageUp, k.PageDown, k.Home, k.End},
		{k.Settings, k.Help, k.Quit},
	}
}
