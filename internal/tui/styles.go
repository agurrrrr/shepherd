package tui

import "github.com/charmbracelet/lipgloss"

// Colors
var (
	ColorPrimary   = lipgloss.Color("#7C3AED") // Purple (brand)
	ColorSecondary = lipgloss.Color("#6B7280") // Gray
	ColorSuccess   = lipgloss.Color("#10B981") // Green
	ColorWarning   = lipgloss.Color("#F59E0B") // Yellow
	ColorError     = lipgloss.Color("#EF4444") // Red
	ColorInfo      = lipgloss.Color("#3B82F6") // Blue
	ColorMuted     = lipgloss.Color("#9CA3AF") // Light gray
	ColorBorder    = lipgloss.Color("#374151") // Border
)

// Status icons
var StatusIcons = map[SheepStatus]string{
	StatusIdle:         "💤",
	StatusWorking:      "🔄",
	StatusWaitingInput: "❓",
	StatusDone:         "✅",
	StatusError:        "❌",
}

// Status colors
var StatusColors = map[SheepStatus]lipgloss.Color{
	StatusIdle:         ColorSecondary,
	StatusWorking:      ColorInfo,
	StatusWaitingInput: ColorWarning,
	StatusDone:         ColorSuccess,
	StatusError:        ColorError,
}

// Styles
var (
	// Header style
	HeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary).
			Padding(0, 1)

	// Border style
	BorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder)

	// Focused border
	FocusedBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorPrimary)

	// Sheep name style
	SheepNameStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary)

	// Project name style
	ProjectNameStyle = lipgloss.NewStyle().
				Foreground(ColorMuted).
				Italic(true)

	// Selected item style
	SelectedStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(ColorPrimary).
			Padding(0, 1)

	// Normal item style
	NormalItemStyle = lipgloss.NewStyle().
			Padding(0, 1)

	// Input field style
	InputStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	// Focused input field style
	FocusedInputStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorPrimary).
				Padding(0, 1)

	// Status bar style
	StatusBarStyle = lipgloss.NewStyle().
			Foreground(ColorMuted).
			Padding(0, 1)

	// Help key style
	HelpKeyStyle = lipgloss.NewStyle().
			Foreground(ColorPrimary).
			Bold(true)

	// Help description style
	HelpDescStyle = lipgloss.NewStyle().
			Foreground(ColorMuted)

	// Output text style
	OutputStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E5E7EB"))

	// Error style
	ErrorStyle = lipgloss.NewStyle().
			Foreground(ColorError)

	// Success style
	SuccessStyle = lipgloss.NewStyle().
			Foreground(ColorSuccess)

	// Warning style
	WarningStyle = lipgloss.NewStyle().
			Foreground(ColorWarning)

	// Title style
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorPrimary).
			MarginBottom(1)

	// Divider style
	DividerStyle = lipgloss.NewStyle().
			Foreground(ColorBorder)
)

// StatusStyle returns the style for a given status
func StatusStyle(status SheepStatus) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(StatusColors[status])
}
