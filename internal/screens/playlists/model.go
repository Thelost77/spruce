package playlists

import (
	"context"
	"slices"
	"strings"

	"github.com/Thelost77/spruce/internal/jellyfin"
	"github.com/Thelost77/spruce/internal/logger"
	"github.com/Thelost77/spruce/internal/screens/library"
	"github.com/Thelost77/spruce/internal/ui"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

var playlistCollator = collate.New(language.Polish, collate.IgnoreCase)

type Level int

const (
	LevelPlaylists Level = iota
	LevelTracks
)

type Model struct {
	client *jellyfin.Client

	level Level

	playlistList list.Model
	trackList    list.Model

	selectedPlaylist jellyfin.Playlist
	playlists        []jellyfin.Playlist
	tracks           []jellyfin.Track

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
				key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add selected to queue")),
				key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "add all to queue")),
				key.NewBinding(key.WithKeys("S"), key.WithHelp("S", "shuffle all to queue")),
			}
		}
		return l
	}

	return Model{
		level:        LevelPlaylists,
		playlistList: initList("Playlists"),
		trackList:    initList("Playlist Tracks"),
		styles:       styles,
	}
}

func (m *Model) SetClient(client *jellyfin.Client) {
	m.client = client
}

func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
	m.playlistList.SetSize(width, height)
	m.trackList.SetSize(width, height)
}

func (m Model) Init() tea.Cmd {
	if m.client != nil && len(m.playlistList.Items()) == 0 {
		return m.fetchPlaylistsCmd()
	}
	return nil
}

func (m Model) fetchPlaylistsCmd() tea.Cmd {
	client := m.client
	return func() tea.Msg {
		logger.Info("fetching playlists")
		playlists, err := client.GetPlaylists(context.Background())
		return PlaylistsLoadedMsg{Playlists: playlists, Err: err}
	}
}

func (m Model) fetchPlaylistTracksCmd(playlistID string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		logger.Info("fetching playlist tracks", "playlistID", playlistID)
		tracks, err := client.GetPlaylistTracks(context.Background(), playlistID)
		return PlaylistTracksLoadedMsg{Tracks: tracks, Err: err}
	}
}

func (m Model) fetchPlaylistTracksForQueueCmd(playlistID string, shuffled bool) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		logger.Info("fetching playlist tracks for queue", "playlistID", playlistID, "shuffled", shuffled)
		tracks, err := client.GetPlaylistTracks(context.Background(), playlistID)
		if err != nil || len(tracks) == 0 {
			return nil
		}
		if shuffled {
			return library.AddShuffledTracksToQueueMsg{Tracks: tracks}
		}
		return library.AddTracksToQueueMsg{Tracks: tracks}
	}
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case PlaylistsLoadedMsg:
		m.loading = false
		m.err = msg.Err
		if msg.Err == nil {
			m.playlists = append([]jellyfin.Playlist(nil), msg.Playlists...)
			slices.SortFunc(m.playlists, func(a, b jellyfin.Playlist) int {
				c := playlistCollator.CompareString(a.Name, b.Name)
				if c != 0 {
					return c
				}
				return strings.Compare(a.ID, b.ID)
			})
			items := make([]list.Item, len(m.playlists))
			for i, p := range m.playlists {
				items[i] = playlistItem{Playlist: p}
			}
			cmd := m.playlistList.SetItems(items)
			m.level = LevelPlaylists
			return m, cmd
		}
		return m, nil

	case PlaylistTracksLoadedMsg:
		m.loading = false
		m.err = msg.Err
		if msg.Err == nil {
			m.tracks = append([]jellyfin.Track(nil), msg.Tracks...)
			items := make([]list.Item, len(m.tracks))
			for i, t := range m.tracks {
				items[i] = trackItem{Track: t}
			}
			m.trackList.Title = m.selectedPlaylist.Name
			cmd := m.trackList.SetItems(items)
			m.level = LevelTracks
			return m, cmd
		}
		return m, nil

	case tea.KeyMsg:
		if m.activeList().FilterState() == list.Filtering {
			break
		}
		switch msg.String() {
		case "esc", "left":
			if m.HasActiveFilter() {
				m.activeList().ResetFilter()
				return m, nil
			}
			if m.level == LevelTracks {
				m.level = LevelPlaylists
				return m, nil
			}
		case "enter", "right":
			if m.level == LevelPlaylists {
				if sel, ok := m.playlistList.SelectedItem().(playlistItem); ok {
					m.selectedPlaylist = sel.Playlist
					m.loading = true
					return m, m.fetchPlaylistTracksCmd(sel.Playlist.ID)
				}
			}
			if m.level == LevelTracks {
				if sel, ok := m.trackList.SelectedItem().(trackItem); ok {
					track := sel.Track
					m.trackList.ResetFilter()
					return m, func() tea.Msg {
						return library.PlayTracksMsg{Tracks: []jellyfin.Track{track}, StartIndex: 0}
					}
				}
			}
		case "a", "A", "S":
			if m.level == LevelPlaylists {
				if sel, ok := m.playlistList.SelectedItem().(playlistItem); ok {
					return m, m.fetchPlaylistTracksForQueueCmd(sel.Playlist.ID, msg.String() == "S")
				}
			}
			if msg.String() == "a" && m.level == LevelTracks {
				if sel, ok := m.trackList.SelectedItem().(trackItem); ok {
					track := sel.Track
					return m, func() tea.Msg { return library.AddTrackToQueueMsg{Track: track} }
				}
			}
			if (msg.String() == "A" || msg.String() == "S") && m.level == LevelTracks && len(m.tracks) > 0 {
				tracks := m.tracks
				return m, func() tea.Msg {
					if msg.String() == "S" {
						return library.AddShuffledTracksToQueueMsg{Tracks: tracks}
					}
					return library.AddTracksToQueueMsg{Tracks: tracks}
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
		m.playlistList, cmd = m.playlistList.Update(msg)
	}
	return m, cmd
}

func (m *Model) activeList() *list.Model {
	if m.level == LevelTracks {
		return &m.trackList
	}
	return &m.playlistList
}

func (m Model) CurrentLevel() Level {
	return m.level
}

func (m Model) IsFiltering() bool {
	return m.activeList().FilterState() == list.Filtering
}

func (m Model) HasActiveFilter() bool {
	return m.activeList().FilterValue() != "" || m.activeList().FilterState() == list.FilterApplied
}

func (m Model) Playlists() []jellyfin.Playlist {
	return m.playlists
}

func (m Model) Tracks() []jellyfin.Track {
	return m.tracks
}

func (m Model) SelectedPlaylist() (jellyfin.Playlist, bool) {
	if sel, ok := m.playlistList.SelectedItem().(playlistItem); ok {
		return sel.Playlist, true
	}
	return jellyfin.Playlist{}, false
}

func (m Model) SelectedTrack() (jellyfin.Track, bool) {
	if sel, ok := m.trackList.SelectedItem().(trackItem); ok {
		return sel.Track, true
	}
	return jellyfin.Track{}, false
}

func (m *Model) SelectPlaylist(playlist jellyfin.Playlist) tea.Cmd {
	m.selectedPlaylist = playlist
	m.loading = true
	return m.fetchPlaylistTracksCmd(playlist.ID)
}
