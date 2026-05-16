package tui

import "github.com/charmbracelet/lipgloss"

// Design tokens for nevinho's terminal UI. Every visible style is built
// from this palette so retuning the look is a single-file change.
//
// Tones are low-saturation hex colours, picked to blend like pi: surfaces
// sit close in value, accents are soft rather than loud, and semantic
// hues (success/danger/warn) keep their meaning without shouting.
var (
	// Surfaces. Backgrounds for the different block types. Close in
	// value so cards and user turns feel like one cohesive transcript.
	bgUser    = lipgloss.Color("#2c2c3a") // user turn bar, dusky lavender-grey
	bgCard    = lipgloss.Color("#181c1a") // tool card body, very dark with a faint green tint
	bgCardErr = lipgloss.Color("#3a1f1f") // tool card body on error, deep muted red
	bgStatus  = lipgloss.Color("#1f1f24") // bottom status bar

	// Foregrounds. Warm off-white primaries, dim warm grey for secondary.
	fgPrimary = lipgloss.Color("#dcdcd6") // primary text on surfaces
	fgBody    = lipgloss.Color("#c6c6c0") // body text on cards
	fgMuted   = lipgloss.Color("#7a7a76") // hints, placeholders, secondary

	// Accent. Soft blue, used for tool headers, selection, spinner.
	accent = lipgloss.Color("#7fa6c9")

	// Semantic. Muted versions of the conventional git/status hues so
	// they read as meaning, not decoration.
	success = lipgloss.Color("#8fb878") // diff add, written content
	danger  = lipgloss.Color("#c97a7a") // errors, diff delete
	warn    = lipgloss.Color("#d4a373") // approval, paused state
	info    = lipgloss.Color("#7fa6c9") // diff hunk headers
)

// Styles composed from the palette. Block renders pin a width on top of
// these so the tint runs edge to edge.
var (
	styleHint   = lipgloss.NewStyle().Foreground(fgMuted)
	styleErr    = lipgloss.NewStyle().Foreground(danger)
	styleStatus = lipgloss.NewStyle().Foreground(fgBody).Background(bgStatus)
	styleSpin   = lipgloss.NewStyle().Foreground(accent)

	// Flat input bar with hairline rules above and below. One row tall,
	// edge to edge, pi-style.
	styleInput     = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true, false, true, false).BorderForeground(fgMuted)
	styleApprove   = lipgloss.NewStyle().Border(lipgloss.NormalBorder(), true, false, true, false).BorderForeground(warn)
	styleApproveLn = lipgloss.NewStyle().Foreground(warn)

	styleSelected = lipgloss.NewStyle().Foreground(accent).Bold(true)
	styleToolHead = lipgloss.NewStyle().Foreground(accent).Bold(true)

	styleUser    = lipgloss.NewStyle().Background(bgUser).Foreground(fgPrimary).Padding(1, 2)
	styleCard    = lipgloss.NewStyle().Background(bgCard).Foreground(fgBody).Padding(1, 2)
	styleCardErr = lipgloss.NewStyle().Background(bgCardErr).Foreground(fgPrimary).Padding(1, 2)

	// Diff lines. Foreground only so the tint sits on top of the card,
	// matching git's calm look rather than a loud full-line background.
	styleDiffAdd  = lipgloss.NewStyle().Foreground(success)
	styleDiffDel  = lipgloss.NewStyle().Foreground(danger)
	styleDiffHunk = lipgloss.NewStyle().Foreground(info)
	styleDiffMeta = lipgloss.NewStyle().Foreground(fgMuted)
)
