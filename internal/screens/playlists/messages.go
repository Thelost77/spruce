package playlists

import (
	"github.com/Thelost77/spruce/internal/jellyfin"
	tea "github.com/charmbracelet/bubbletea"
)

type PlaylistsLoadedMsg struct {
	Playlists []jellyfin.Playlist
	Err       error
}

type PlaylistTracksLoadedMsg struct {
	Tracks []jellyfin.Track
	Err    error
}

// RefilterMsg routes an asynchronous list refilter back to the playlists model.
type RefilterMsg struct {
	Msg tea.Msg
}
