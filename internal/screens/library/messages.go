package library

import "github.com/Thelost77/spruce/internal/jellyfin"

// PlayTracksMsg signals to the parent app that playback of tracks should begin.
type PlayTracksMsg struct {
	Tracks     []jellyfin.Track
	StartIndex int
}

// ArtistsLoadedMsg is received when artists are loaded from Jellyfin.
type ArtistsLoadedMsg struct {
	Artists []jellyfin.Artist
	Err     error
}

// AlbumsLoadedMsg is received when albums are loaded for an artist.
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
