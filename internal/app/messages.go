package app

type Screen int

const (
	ScreenLogin Screen = iota
	ScreenLibrary
	ScreenQueue
)

// SwitchScreenMsg requests switching to a specific screen.
type SwitchScreenMsg struct {
	Screen Screen
}
