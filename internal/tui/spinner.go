package tui

import (
	"math/rand/v2"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var spinnerCaptions = []string{
	"Reticulating splines",
	"Reverse engineering the universe",
	"Mining bitcoins... kidding",
	"Untangling pointers",
	"Negotiating with the compiler",
	"Compiling thoughts",
	"Asking nicely",
	"Consulting the rubber duck",
	"Counting electrons",
	"Polishing pixels",
	"Petting the cat",
	"Loading more dots",
}

type rotateCaptionMsg struct{}

func rotateCaptionEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return rotateCaptionMsg{} })
}

func randomCaption() string {
	return spinnerCaptions[rand.IntN(len(spinnerCaptions))]
}
