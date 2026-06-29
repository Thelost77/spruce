package library

import (
	"context"
	"fmt"

	"github.com/Thelost77/spruce/internal/jellyfin"
	"github.com/Thelost77/spruce/internal/logger"
	"github.com/Thelost77/spruce/internal/ui"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

type Level int

const (
	LevelArtists Level = iota
	LevelAlbums
	LevelTracks
)

type Model struct {
	client    *jellyfin.Client
	libraryID string

	level Level

	artistList list.Model
	albumList  list.Model
	trackList  list.Model

	selectedArtist jellyfin.Artist
	selectedAlbum  jellyfin.Album
	artists        []jellyfin.Artist
	albums         []jellyfin.Album
	tracks         []jellyfin.Track
	allTracks      []jellyfin.Track

	loading bool
	err     error
	width   int
	height  int
	styles  ui.Styles
}

func New(styles ui.Styles) Model {
	del := list.NewDefaultDelegate()
	del.Styles.SelectedTitle = del.Styles.SelectedTitle.Foreground(styles.Accent.GetForeground()).BorderForeground(styles.Accent.GetForeground())
	del.Styles.SelectedDesc = del.Styles.SelectedDesc.Foreground(styles.Muted.GetForeground()).BorderForeground(styles.Accent.GetForeground())

	initList := func(title string) list.Model {
		l := list.New(nil, del, 0, 0)
		l.KeyMap.Quit.SetKeys("q")
		l.Title = title
		l.SetShowStatusBar(true)
		l.SetFilteringEnabled(true)
		l.AdditionalFullHelpKeys = func() []key.Binding {
			return []key.Binding{
				key.NewBinding(key.WithKeys("h", "left", "esc"), key.WithHelp("h/esc", "back")),
			}
		}
		return l
	}

	return Model{
		level:      LevelArtists,
		artistList: initList("Artists"),
		albumList:  initList("Albums"),
		trackList:  initList("Tracks"),
		styles:     styles,
	}
}

func (m *Model) SetClient(client *jellyfin.Client, libraryID string) {
	m.client = client
	m.libraryID = libraryID
}

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.artistList.SetSize(width, height)
	m.albumList.SetSize(width, height)
	m.trackList.SetSize(width, height)
}

func (m Model) Init() tea.Cmd {
	if m.client != nil && m.libraryID != "" && len(m.artistList.Items()) == 0 {
		return m.fetchArtistsCmd()
	}
	return nil
}

func (m Model) fetchArtistsCmd() tea.Cmd {
	client := m.client
	libID := m.libraryID
	return func() tea.Msg {
		logger.Info("fetching artists", "libraryID", libID)
		artists, err := client.GetArtists(context.Background(), libID)
		return ArtistsLoadedMsg{Artists: artists, Err: err}
	}
}

func (m Model) fetchAlbumsCmd(artistID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		logger.Info("fetching albums", "artistID", artistID)
		albums, err := client.GetAlbums(context.Background(), artistID)
		return AlbumsLoadedMsg{Albums: albums, Err: err}
	}
}

func (m Model) fetchTracksCmd(albumID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		logger.Info("fetching tracks", "albumID", albumID)
		tracks, err := client.GetTracks(context.Background(), albumID)
		return TracksLoadedMsg{Tracks: tracks, Err: err}
	}
}

func (m Model) FetchAllTracksCmd() tea.Cmd {
	client := m.client
	libID := m.libraryID
	return func() tea.Msg {
		logger.Info("fetching all library tracks", "libraryID", libID)
		tracks, err := client.GetAllTracks(context.Background(), libID)
		return AllTracksLoadedMsg{Tracks: tracks, Err: err}
	}
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ArtistsLoadedMsg:
		m.loading = false
		m.err = msg.Err
		if msg.Err == nil {
			m.artists = msg.Artists
			items := make([]list.Item, len(msg.Artists))
			for i, a := range msg.Artists {
				items[i] = artistItem{Artist: a}
			}
			cmd := m.artistList.SetItems(items)
			return m, cmd
		}
		return m, nil

	case AlbumsLoadedMsg:
		m.loading = false
		m.err = msg.Err
		if msg.Err == nil {
			m.albums = msg.Albums
			items := make([]list.Item, len(msg.Albums))
			for i, a := range msg.Albums {
				items[i] = albumItem{Album: a}
			}
			m.albumList.Title = fmt.Sprintf("Albums — %s", m.selectedArtist.Name)
			cmd := m.albumList.SetItems(items)
			m.level = LevelAlbums
			return m, cmd
		}
		return m, nil

	case TracksLoadedMsg:
		m.loading = false
		m.err = msg.Err
		if msg.Err == nil {
			m.tracks = msg.Tracks
			items := make([]list.Item, len(msg.Tracks))
			for i, t := range msg.Tracks {
				items[i] = trackItem{Track: t}
			}
			m.trackList.Title = fmt.Sprintf("Tracks — %s", m.selectedAlbum.Name)
			cmd := m.trackList.SetItems(items)
			m.level = LevelTracks
			return m, cmd
		}
		return m, nil

	case AllTracksLoadedMsg:
		if msg.Err == nil {
			m.allTracks = msg.Tracks
		}
		return m, nil

	case tea.KeyMsg:
		if m.activeList().FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "h", "left", "esc":
			if m.level == LevelTracks {
				m.level = LevelAlbums
				return m, nil
			} else if m.level == LevelAlbums {
				m.level = LevelArtists
				return m, nil
			}
			// If already at Artists level, parent handles esc/back

		case "enter":
			switch m.level {
			case LevelArtists:
				if sel, ok := m.artistList.SelectedItem().(artistItem); ok {
					m.selectedArtist = sel.Artist
					m.loading = true
					return m, m.fetchAlbumsCmd(sel.Artist.ID)
				}
			case LevelAlbums:
				if sel, ok := m.albumList.SelectedItem().(albumItem); ok {
					m.selectedAlbum = sel.Album
					m.loading = true
					return m, m.fetchTracksCmd(sel.Album.ID)
				}
			case LevelTracks:
				idx := m.trackList.Index()
				if len(m.tracks) > 0 && idx >= 0 && idx < len(m.tracks) {
					return m, func() tea.Msg {
						return PlayTracksMsg{
							Tracks:     m.tracks,
							StartIndex: idx,
						}
					}
				}
			}
		}
	}

	var cmd tea.Cmd
	switch m.level {
	case LevelArtists:
		m.artistList, cmd = m.artistList.Update(msg)
	case LevelAlbums:
		m.albumList, cmd = m.albumList.Update(msg)
	case LevelTracks:
		m.trackList, cmd = m.trackList.Update(msg)
	}
	return m, cmd
}

func (m *Model) activeList() *list.Model {
	switch m.level {
	case LevelAlbums:
		return &m.albumList
	case LevelTracks:
		return &m.trackList
	default:
		return &m.artistList
	}
}

func (m Model) CurrentLevel() Level {
	return m.level
}

func (m Model) Loading() bool {
	return m.loading
}

func (m Model) Error() error {
	return m.err
}

func (m Model) Artists() []jellyfin.Artist {
	return m.artists
}

func (m Model) Albums() []jellyfin.Album {
	return m.albums
}

func (m Model) Tracks() []jellyfin.Track {
	return m.tracks
}

func (m Model) AllTracks() []jellyfin.Track {
	return m.allTracks
}

func (m *Model) SetAllTracks(tracks []jellyfin.Track) {
	m.allTracks = tracks
}

func (m Model) IsFiltering() bool {
	return m.activeList().FilterState() == list.Filtering
}

func (m *Model) SelectArtist(artist jellyfin.Artist) tea.Cmd {
	m.selectedArtist = artist
	m.loading = true
	return m.fetchAlbumsCmd(artist.ID)
}
