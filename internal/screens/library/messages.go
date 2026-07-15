package library

import (
	"github.com/Thelost77/spruce/internal/jellyfin"
	tea "github.com/charmbracelet/bubbletea"
)

// PlayTracksMsg signals to the parent app that playback of tracks should begin.
type PlayTracksMsg struct {
	Tracks     []jellyfin.Track
	StartIndex int
}

// AddTrackToQueueMsg signals that a single track should be appended to the queue.
type AddTrackToQueueMsg struct {
	Track jellyfin.Track
}

// AddTracksToQueueMsg signals that multiple tracks should be appended to the queue.
type AddTracksToQueueMsg struct {
	Tracks []jellyfin.Track
}

// AlbumsLoadedMsg is received when albums are loaded from Jellyfin.
type AlbumsLoadedMsg struct {
	Albums []jellyfin.Album
	Err    error
}

// TracksLoadedMsg is received when tracks are loaded for an album.
type TracksLoadedMsg struct {
	AlbumID string
	Tracks  []jellyfin.Track
	Err     error
}

// AllTracksLoadedMsg is received when all tracks in the library are loaded.
type AllTracksLoadedMsg struct {
	Tracks []jellyfin.Track
	Err    error
}

// FavoriteChangedMsg reports the server-confirmed favorite state for a track.
type FavoriteChangedMsg struct {
	TrackID    string
	IsFavorite bool
	Err        error
}

// AlbumFavoriteChangedMsg reports the server-confirmed favorite state for an album.
type AlbumFavoriteChangedMsg struct {
	AlbumID    string
	IsFavorite bool
	Err        error
}

// RefilterMsg routes an asynchronous list refilter back to the library model.
type RefilterMsg struct {
	Msg tea.Msg
}

// EditMetadataMsg requests opening the metadata editor for a track or album.
type EditMetadataMsg struct {
	ItemID   string
	ItemType string // "Track" or "Album"
	Track    *jellyfin.Track
	Album    *jellyfin.Album
}
