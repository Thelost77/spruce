package mpris

// ModelAccessor provides read-only access to spruce's playback state.
type ModelAccessor interface {
	IsPlaying() bool
	IsPaused() bool
	HasActiveItem() bool
	CurrentTitle() string
	CurrentAuthors() []string
	CurrentItemID() string
	PlayerPosition() float64
	PlayerDuration() float64
	PlayerVolume() int
	PlayerSpeed() float64
	QueueLength() int
}
