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

// NavigateMsg requests navigation to a different screen, pushing the current
// one onto the back stack.
type NavigateMsg struct {
	Target Screen
}

// BackMsg requests navigation back to the previous screen on the back stack.
type BackMsg struct{}

// SleepTimerExpiredMsg fires when the sleep timer reaches zero.
type SleepTimerExpiredMsg struct {
	Generation uint64
}

