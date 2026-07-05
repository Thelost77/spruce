package playlists

import "github.com/Thelost77/spruce/internal/jellyfin"

type PlaylistsLoadedMsg struct {
	Playlists []jellyfin.Playlist
	Err       error
}

type PlaylistTracksLoadedMsg struct {
	Tracks []jellyfin.Track
	Err    error
}
