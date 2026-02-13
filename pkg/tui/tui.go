package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFFFFF")).
			Background(lipgloss.Color("#5A3BD1")).
			Padding(1, 2).
			MarginBottom(1).
			Border(lipgloss.RoundedBorder(), true)

	infoStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#38BDF8")).
			MarginLeft(2)

	successStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#10B981")).
			Bold(true)

	labelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#94A3B8")).
			MarginLeft(2)

	valueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#E2E8F0"))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#F43F5E")).
			Bold(true)
)

type Model struct {
	Mode            string
	IP              string
	Port            int
	Magnet          string
	Torrent         *torrent.Torrent
	Progress        progress.Model
	Quitting        bool
	IsComplete      bool
	HTTPAddr        string // URL for binary distribution
	IsHashing       bool
	HashingProgress float64
	// Stats for speed calculation
	LastBytesRead    int64
	LastBytesWritten int64
	DownSpeed        int64
	UpSpeed          int64
}

type TickMsg struct{}
type HashingProgressMsg float64
type TorrentLoadedMsg struct {
	Torrent *torrent.Torrent
	Magnet  string
}

func Tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return TickMsg{}
	})
}

func (m Model) Init() tea.Cmd {
	return Tick()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC || msg.String() == "q" {
			m.Quitting = true
			return m, tea.Quit
		}
	case tea.WindowSizeMsg:
		m.Progress.Width = msg.Width - 4
		return m, nil
	case HashingProgressMsg:
		m.IsHashing = true
		m.HashingProgress = float64(msg)
		return m, nil

	case TorrentLoadedMsg:
		m.IsHashing = false
		m.Torrent = msg.Torrent
		m.Magnet = msg.Magnet
		return m, nil

	case TickMsg:
		if m.Torrent != nil {
			stats := m.Torrent.Stats()
			currentRead := stats.BytesReadUsefulData.Int64()
			currentWrite := stats.BytesWrittenData.Int64()

			// Calculate speed if we have previous values
			if m.LastBytesRead > 0 {
				m.DownSpeed = currentRead - m.LastBytesRead
			}
			if m.LastBytesWritten > 0 {
				m.UpSpeed = currentWrite - m.LastBytesWritten
			}

			m.LastBytesRead = currentRead
			m.LastBytesWritten = currentWrite

			if m.Torrent.Info() != nil {
				prog := float64(m.Torrent.BytesCompleted()) / float64(m.Torrent.Length())
				if prog >= 1.0 && m.Torrent.Length() > 0 {
					m.IsComplete = true
				}
				// Safely start/resume download once info is available
				m.Torrent.DownloadAll()
			}
		}
		return m, Tick()
	}
	return m, nil
}

func (m Model) View() string {
	if m.Quitting {
		return "\n  Au revoir!\n\n"
	}

	var s strings.Builder

	s.WriteString(titleStyle.Render(fmt.Sprintf(" Torrent Class - %s ", strings.ToUpper(m.Mode))))
	s.WriteString("\n\n")

	s.WriteString(infoStyle.Render(fmt.Sprintf("Local IP: %s", m.IP)))
	s.WriteString("\n")
	s.WriteString(infoStyle.Render(fmt.Sprintf("Port:     %d", m.Port)))
	s.WriteString("\n\n")

	// Status Line
	s.WriteString(labelStyle.Render("Status:    "))
	if m.IsHashing {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FACC15")).Render("HASHING FILES..."))
	} else if m.Mode == "seed" {
		s.WriteString(successStyle.Render("SEEDING"))
	} else {
		if m.IsComplete {
			s.WriteString(successStyle.Render("✓ COMPLETE"))
		} else if m.Torrent == nil {
			s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FACC15")).Render("WAITING FOR MAGNET..."))
		} else {
			s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FACC15")).Render("DOWNLOADING"))
		}
	}
	s.WriteString("\n")

	// Info section (Magnet or Hashing Progress)
	if m.IsHashing {
		s.WriteString("\n")
		s.WriteString(labelStyle.Render("Progress:  "))
		s.WriteString(valueStyle.Render(fmt.Sprintf("%.1f%%", m.HashingProgress*100)))
		s.WriteString("\n")
		s.WriteString("  " + m.Progress.ViewAs(m.HashingProgress))
		s.WriteString("\n")
	} else if m.Mode == "seed" {
		s.WriteString(labelStyle.Render("Magnet:    "))
		displayMagnet := "None"
		if len(m.Magnet) > 20 {
			displayMagnet = m.Magnet[:20] + "..."
		} else if len(m.Magnet) > 0 {
			displayMagnet = m.Magnet
		}
		s.WriteString(valueStyle.Render(displayMagnet))
		s.WriteString("\n")
	} else {
		s.WriteString("\n")
	}

	s.WriteString("\n")

	// Deployment Info - Only show this once
	if m.HTTPAddr != "" {
		s.WriteString(labelStyle.Render("Deploy:    "))
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#34D399")).Underline(true).Render(m.HTTPAddr))
		s.WriteString("\n\n")
	}

	// Active Torrent Stats
	if m.Torrent != nil && !m.IsHashing {
		stats := m.Torrent.Stats()

		if m.Torrent.Info() == nil {
			s.WriteString(labelStyle.Render("Metadata:  "))
			s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FACC15")).Render("WAITING..."))
			s.WriteString("\n\n")
		} else {
			total := float64(m.Torrent.Length())
			completed := float64(m.Torrent.BytesCompleted())
			prog := 0.0
			if total > 0 {
				prog = completed / total
			}

			s.WriteString(labelStyle.Render("Progress:  "))
			s.WriteString(valueStyle.Render(fmt.Sprintf("%.1f%%", prog*100)))
			s.WriteString("\n")
			s.WriteString("  " + m.Progress.ViewAs(prog))
			s.WriteString("\n\n")
		}

		s.WriteString(labelStyle.Render("Peers:      "))
		s.WriteString(valueStyle.Render(fmt.Sprintf("%d", stats.ActivePeers)))
		s.WriteString("\n\n")

		s.WriteString(labelStyle.Render("Downloaded: "))
		s.WriteString(valueStyle.Render(humanizeBytes(stats.BytesReadUsefulData.Int64())))
		s.WriteString("\n")
		s.WriteString(labelStyle.Render("Down Speed: "))
		s.WriteString(valueStyle.Render(humanizeBytes(m.DownSpeed) + "/s"))
		s.WriteString("\n\n")

		s.WriteString(labelStyle.Render("Uploaded:   "))
		s.WriteString(valueStyle.Render(humanizeBytes(stats.BytesWrittenData.Int64())))
		s.WriteString("\n")
		s.WriteString(labelStyle.Render("Up Speed:   "))
		s.WriteString(valueStyle.Render(humanizeBytes(m.UpSpeed) + "/s"))
		s.WriteString("\n")
	}

	s.WriteString("\n\n")
	s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("  Appuyez sur 'q' pour quitter"))

	return s.String()
}

func humanizeBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
