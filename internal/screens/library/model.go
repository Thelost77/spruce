package library

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Thelost77/spruce/internal/jellyfin"
	"github.com/Thelost77/spruce/internal/logger"
	"github.com/Thelost77/spruce/internal/ui"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

type Level int

const (
	LevelAlbums Level = iota
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
	allTracksErr   error

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
		l.KeyMap.PrevPage.SetKeys("pgup", "b", "u")
		l.KeyMap.NextPage.SetKeys("pgdown", "f")
		l.Title = title
		l.SetShowTitle(false)
		l.SetShowHelp(false)
		l.SetShowStatusBar(true)
		l.SetFilteringEnabled(true)
		l.AdditionalFullHelpKeys = func() []key.Binding {
			return []key.Binding{
				key.NewBinding(key.WithKeys("esc", "left"), key.WithHelp("esc", "back")),
				key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add track to queue")),
				key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "add album to queue")),
				key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "add album shuffled")),
			}
		}
		return l
	}

	return Model{
		level:      LevelAlbums,
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
	if m.client != nil && m.libraryID != "" && len(m.albumList.Items()) == 0 {
		return m.fetchAllAlbumsCmd()
	}
	return nil
}

func (m Model) fetchAllAlbumsCmd() tea.Cmd {
	client := m.client
	libID := m.libraryID
	return func() tea.Msg {
		logger.Info("fetching all albums", "libraryID", libID)
		albums, err := client.GetAllAlbums(context.Background(), libID)
		return AlbumsLoadedMsg{Albums: albums, Err: err}
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

type AddShuffledTracksToQueueMsg struct {
	Tracks []jellyfin.Track
}

func (m Model) fetchAlbumTracksForQueueCmd(albumID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		logger.Info("fetching tracks for queue", "albumID", albumID)
		if client == nil {
			return nil
		}
		tracks, err := client.GetTracks(context.Background(), albumID)
		if err != nil || len(tracks) == 0 {
			return nil
		}
		return AddTracksToQueueMsg{Tracks: tracks}
	}
}

func (m Model) fetchAlbumTracksForShuffledQueueCmd(albumID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		logger.Info("fetching shuffled tracks for queue", "albumID", albumID)
		if client == nil {
			return nil
		}
		tracks, err := client.GetTracks(context.Background(), albumID)
		if err != nil || len(tracks) == 0 {
			return nil
		}
		return AddShuffledTracksToQueueMsg{Tracks: tracks}
	}
}

func (m Model) FetchAllTracksCmd() tea.Cmd {
	client := m.client
	libID := m.libraryID
	return func() tea.Msg {
		logger.Info("fetching all library tracks", "libraryID", libID)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		tracks, err := client.GetAllTracks(ctx, libID)
		return AllTracksLoadedMsg{Tracks: tracks, Err: err}
	}
}

// RefreshCmd re-fetches the current level items from Jellyfin.
func (m *Model) RefreshCmd() tea.Cmd {
	m.loading = true
	if m.allTracks != nil {
		return m.FetchAllTracksCmd()
	}
	switch m.level {
	case LevelAlbums:
		return m.fetchAllAlbumsCmd()
	case LevelTracks:
		if m.selectedAlbum.ID != "" {
			return m.fetchTracksCmd(m.selectedAlbum.ID)
		}
		return m.FetchAllTracksCmd()
	}
	return nil
}

// PatchTrack updates a single track in all in-memory lists without any network call.
func (m *Model) PatchTrack(id, name string, artists []string, album string) {
	patch := func(t *jellyfin.Track) {
		if name != "" {
			t.Name = name
		}
		if artists != nil {
			t.Artists = artists
		}
		if album != "" {
			t.Album = album
		}
	}
	for i := range m.tracks {
		if m.tracks[i].ID == id {
			patch(&m.tracks[i])
			items := m.trackList.Items()
			for j, item := range items {
				if ti, ok := item.(trackItem); ok && ti.Track.ID == id {
					patch(&ti.Track)
					items[j] = ti
				}
			}
			m.trackList.SetItems(items)
		}
	}
	for i := range m.allTracks {
		if m.allTracks[i].ID == id {
			patch(&m.allTracks[i])
		}
	}
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case AlbumsLoadedMsg:
		m.loading = false
		m.err = msg.Err
		if msg.Err == nil {
			m.albums = append([]jellyfin.Album(nil), msg.Albums...)
			slices.SortFunc(m.albums, func(a, b jellyfin.Album) int {
				return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
			})
			items := make([]list.Item, len(m.albums))
			for i, a := range m.albums {
				items[i] = albumItem{Album: a}
			}
			m.albumList.Title = "Albums"
			cmd := m.albumList.SetItems(items)
			m.level = LevelAlbums
			return m, cmd
		}
		return m, nil

	case TracksLoadedMsg:
		m.loading = false
		m.err = msg.Err
		if msg.Err == nil {
			m.tracks = append([]jellyfin.Track(nil), msg.Tracks...)
			slices.SortFunc(m.tracks, func(a, b jellyfin.Track) int {
				return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
			})
			items := make([]list.Item, len(m.tracks))
			for i, t := range m.tracks {
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
			m.allTracks = append([]jellyfin.Track(nil), msg.Tracks...)
			slices.SortFunc(m.allTracks, func(a, b jellyfin.Track) int {
				return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
			})
			m.allTracksErr = nil
		} else {
			m.allTracksErr = msg.Err
		}
		return m, nil

	case tea.KeyMsg:
		if m.activeList().FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "m":
			if m.level == LevelAlbums {
				if sel, ok := m.albumList.SelectedItem().(albumItem); ok {
					alb := sel.Album
					return m, func() tea.Msg {
						return EditMetadataMsg{ItemID: alb.ID, ItemType: "Album", Album: &alb}
					}
				}
			} else if m.level == LevelTracks {
				if sel, ok := m.trackList.SelectedItem().(trackItem); ok {
					trk := sel.Track
					return m, func() tea.Msg {
						return EditMetadataMsg{ItemID: trk.ID, ItemType: "Track", Track: &trk}
					}
				}
			}
		case "r":
			if m.allTracksErr != nil {
				m.allTracksErr = nil
				return m, m.FetchAllTracksCmd()
			}
		case "esc", "left":
			if m.HasActiveFilter() {
				if al := m.activeList(); al != nil {
					al.ResetFilter()
				}
				return m, nil
			}
			if m.level == LevelTracks {
				m.level = LevelAlbums
				return m, nil
			}
			// If already at Albums level, parent handles esc/back

		case "enter", "right":
			switch m.level {
			case LevelAlbums:
				if sel, ok := m.albumList.SelectedItem().(albumItem); ok {
					m.selectedAlbum = sel.Album
					m.loading = true
					return m, m.fetchTracksCmd(sel.Album.ID)
				}
			case LevelTracks:
				if sel, ok := m.trackList.SelectedItem().(trackItem); ok {
					track := sel.Track
					m.trackList.ResetFilter()
					return m, func() tea.Msg {
						return PlayTracksMsg{
							Tracks:     []jellyfin.Track{track},
							StartIndex: 0,
						}
					}
				}
			}
		case "a", "A", "S":
			if m.level == LevelAlbums {
				if sel, ok := m.albumList.SelectedItem().(albumItem); ok {
					if msg.String() == "S" {
						return m, m.fetchAlbumTracksForShuffledQueueCmd(sel.Album.ID)
					}
					return m, m.fetchAlbumTracksForQueueCmd(sel.Album.ID)
				}
			}
			if msg.String() == "a" && m.level == LevelTracks {
				if sel, ok := m.trackList.SelectedItem().(trackItem); ok {
					track := sel.Track
					return m, func() tea.Msg {
						return AddTrackToQueueMsg{Track: track}
					}
				}
			}
			if (msg.String() == "A" || msg.String() == "S") && m.level == LevelTracks && len(m.tracks) > 0 {
				tracks := m.tracks
				return m, func() tea.Msg {
					if msg.String() == "S" {
						return AddShuffledTracksToQueueMsg{Tracks: tracks}
					}
					return AddTracksToQueueMsg{Tracks: tracks}
				}
			}
		case "L":
			al := m.activeList()
			before := al.GlobalIndex()
			al.NextPage()
			if al.GlobalIndex() == before {
				al.GoToEnd()
			}
			return m, nil
		case "H":
			al := m.activeList()
			before := al.GlobalIndex()
			al.PrevPage()
			if al.GlobalIndex() == before {
				al.GoToStart()
			}
			return m, nil
		}
	}

	var cmd tea.Cmd
	switch m.level {
	case LevelTracks:
		m.trackList, cmd = m.trackList.Update(msg)
	default:
		m.albumList, cmd = m.albumList.Update(msg)
	}
	return m, cmd
}

func (m *Model) activeList() *list.Model {
	switch m.level {
	case LevelTracks:
		return &m.trackList
	default:
		return &m.albumList
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

func (m Model) HasActiveFilter() bool {
	return m.activeList().FilterValue() != "" || m.activeList().FilterState() == list.FilterApplied
}

func (m *Model) SelectAlbum(album jellyfin.Album) tea.Cmd {
	m.selectedAlbum = album
	m.loading = true
	return m.fetchTracksCmd(album.ID)
}
