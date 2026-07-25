package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// One accent (Salesforce sky blue); every other color is semantic and only
// appears on status-bearing content. Chrome stays monochrome.
var (
	colorAccent   = lipgloss.AdaptiveColor{Light: "#0176D3", Dark: "#57A3FD"}
	colorOnAccent = lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#0B1220"}
	colorDim      = lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#8B949E"}
	colorBand     = lipgloss.AdaptiveColor{Light: "#EAF1F8", Dark: "#161B22"}
	colorOK       = lipgloss.AdaptiveColor{Light: "#1F7A33", Dark: "#3FB950"}
	colorWarn     = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#D29922"}
	colorErr      = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#F85149"}

	styleLogoChip = lipgloss.NewStyle().Bold(true).
			Foreground(colorOnAccent).Background(colorAccent)
	styleVersion = lipgloss.NewStyle().Foreground(colorDim)
	styleTab     = lipgloss.NewStyle().Foreground(colorDim)
	styleTabOn   = lipgloss.NewStyle().Foreground(colorOnAccent).Background(colorAccent).Bold(true)

	styleBand      = lipgloss.NewStyle().Background(colorBand)
	styleStatusDim = lipgloss.NewStyle().Foreground(colorDim).Background(colorBand)
	styleToastOK   = lipgloss.NewStyle().Foreground(colorOK).Background(colorBand)
	styleToastWarn = lipgloss.NewStyle().Foreground(colorWarn).Background(colorBand)
	styleToastErr  = lipgloss.NewStyle().Bold(true).Foreground(colorErr).Background(colorBand)

	styleProdBadge = lipgloss.NewStyle().Bold(true).
			Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#FFFFFF"}).
			Background(colorErr)
	styleProdText = lipgloss.NewStyle().Bold(true).Foreground(colorErr)

	styleTableHeader = lipgloss.NewStyle().Bold(true).Foreground(colorAccent)
	styleRowSelected = lipgloss.NewStyle().Bold(true).
				Foreground(colorOnAccent).Background(colorAccent)
	styleDim     = lipgloss.NewStyle().Foreground(colorDim)
	styleOK      = lipgloss.NewStyle().Foreground(colorOK)
	styleWarn    = lipgloss.NewStyle().Foreground(colorWarn)
	styleErrText = lipgloss.NewStyle().Foreground(colorErr)
	styleTitle   = lipgloss.NewStyle().Bold(true)

	styleOverlay = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorAccent).
			Padding(0, 1)

	styleEditorFocused = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).Padding(0, 1)
)

// statusCellStyle colors a status-bearing cell by its meaning; anything
// unrecognized stays unstyled so tables never turn into confetti.
func statusCellStyle(value string) lipgloss.Style {
	v := strings.ToLower(value)
	switch {
	case v == "":
		return lipgloss.NewStyle()
	case strings.Contains(v, "connected"), strings.Contains(v, "active"),
		strings.Contains(v, "succeeded"), strings.Contains(v, "success"), strings.Contains(v, "completed"):
		return styleOK
	case strings.Contains(v, "expired"), strings.Contains(v, "fail"),
		strings.Contains(v, "error"), strings.Contains(v, "canceled"), strings.Contains(v, "cancelled"):
		return styleErrText
	case strings.Contains(v, "progress"), strings.Contains(v, "pending"),
		strings.Contains(v, "queued"), strings.Contains(v, "hold"):
		return styleWarn
	default:
		return lipgloss.NewStyle()
	}
}

// orgTypeCellStyle gives each org flavor a stable identity color.
func orgTypeCellStyle(value string) lipgloss.Style {
	switch value {
	case "PRODUCTION":
		return styleProdText
	case "prod?":
		// Nothing rules out production yet — the org has not been asked.
		return styleWarn
	case "developer", "local":
		return styleDim
	case "scratch":
		return styleWarn
	case "devhub":
		return lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	case "sandbox":
		return styleDim
	default:
		return lipgloss.NewStyle()
	}
}
