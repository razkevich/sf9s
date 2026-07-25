package ui

import "github.com/charmbracelet/lipgloss"

var (
	colorAccent = lipgloss.AdaptiveColor{Light: "#0176D3", Dark: "#57A3FD"}
	colorDim    = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8B949E"}
	colorOK     = lipgloss.AdaptiveColor{Light: "#1F7A33", Dark: "#3FB950"}
	colorWarn   = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#D29922"}
	colorErr    = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F85149"}

	styleTopBar = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleTab    = lipgloss.NewStyle().Foreground(colorDim).Padding(0, 1)
	styleTabOn  = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Padding(0, 1).Underline(true)

	styleStatusOrg  = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleStatusDim  = lipgloss.NewStyle().Foreground(colorDim)
	styleStatusOK   = lipgloss.NewStyle().Foreground(colorOK)
	styleStatusErr  = lipgloss.NewStyle().Foreground(colorErr)
	styleStatusWarn = lipgloss.NewStyle().Foreground(colorWarn)

	styleTableHeader = lipgloss.NewStyle().Bold(true).Foreground(colorAccent).Underline(true)
	styleRowSelected = lipgloss.NewStyle().Reverse(true)
	styleDim         = lipgloss.NewStyle().Foreground(colorDim)
	styleOK          = lipgloss.NewStyle().Foreground(colorOK)
	styleWarn        = lipgloss.NewStyle().Foreground(colorWarn)
	styleErrText     = lipgloss.NewStyle().Foreground(colorErr)
	styleTitle       = lipgloss.NewStyle().Bold(true)

	styleOverlay = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAccent).
			Padding(0, 1)
)
