package library

import "github.com/Thelost77/spruce/internal/jellyfin"

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
	Tracks []jellyfin.Track
	Err    error
}

// AllTracksLoadedMsg is received when all tracks in the library are loaded.
type AllTracksLoadedMsg struct {
	Tracks []jellyfin.Track
	Err    error
}

// EditMetadataMsg requests opening the metadata editor for a track or album.
type EditMetadataMsg struct {
	ItemID   string
	ItemType string // "Track" or "Album"
	Track    *jellyfin.Track
	Album    *jellyfin.Album
}
