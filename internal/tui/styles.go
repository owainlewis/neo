package tui

import "charm.land/lipgloss/v2"

var (
	colMuted  = lipgloss.Color("244")
	colDim    = lipgloss.Color("240")
	colAccent = lipgloss.Color("12")
	colTool   = lipgloss.Color("14")
	colOK     = lipgloss.Color("10")
	colErr    = lipgloss.Color("9")
	// Status-dot palette. The dot is always present in the status line; its
	// color encodes what the agent is doing.
	colDotReady    = lipgloss.Color("42")  // green  — idle, awaiting input
	colDotThinking = lipgloss.Color("208") // orange — model is thinking
	colDotTool     = lipgloss.Color("14")  // cyan   — a tool is in flight
	// Splash banner colour. Picked to feel "neo blue" — saturated but not
	// neon. xterm-256 39 is DeepSkyBlue1 (rgb 0/175/255).
	colBanner  = lipgloss.Color("39")
	colCardBg  = lipgloss.Color("236")
	colToolBg  = lipgloss.Color("235")
	colInputBg = lipgloss.Color("234")

	styMuted    = lipgloss.NewStyle().Foreground(colMuted)
	styDim      = lipgloss.NewStyle().Foreground(colDim)
	styAccent   = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	styTool     = lipgloss.NewStyle().Foreground(colTool).Bold(true)
	styOK       = lipgloss.NewStyle().Foreground(colOK)
	styErr      = lipgloss.NewStyle().Foreground(colErr)
	styThinking = lipgloss.NewStyle().Foreground(colMuted).Italic(true)

	styCardTool = lipgloss.NewStyle().
			Background(colToolBg).
			Padding(0, 1)

	styCardResult = lipgloss.NewStyle().
			Background(colCardBg).
			Padding(0, 1)

	styCardErr = lipgloss.NewStyle().
			Background(lipgloss.Color("52")).
			Padding(0, 1)

	styCardWarn = lipgloss.NewStyle().
			Background(lipgloss.Color("58")).
			Padding(0, 1)

	// styInputBar renders the chat input as a single solid-color block with
	// no borders — matching the OpenAI Codex composer. The vertical padding
	// keeps the one-row textarea from feeling cramped now that the framing
	// borders are gone.
	styInputBar = lipgloss.NewStyle().
			Background(colInputBg).
			Padding(1, 1)

	styPicker = lipgloss.NewStyle().
			Padding(0, 1)

	styPickerSelected = lipgloss.NewStyle().
				Foreground(colTool).
				Bold(true)

	styPickerCommand = lipgloss.NewStyle().
				Bold(true)

	styFooter = lipgloss.NewStyle().
			Foreground(colMuted)
)
