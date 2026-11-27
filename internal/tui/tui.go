package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"torrent-class/internal/netutils"
	"torrent-class/internal/utils"
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

	warnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FACC15")).
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
	Version          string
	Mode             string
	IP               string
	Port             int
	SeederIP         string
	Magnet           string
	Torrent          *torrent.Torrent
	Progress         progress.Model
	Quitting         bool
	ConfirmQuit      bool
	IsComplete       bool
	HTTPAddr         string // URL for binary distribution
	IsHashing        bool
	HashingProgress  float64
	LastBytesRead    int64
	LastBytesWritten int64
	DownSpeed        int64
	UpSpeed          int64
	DownSpeedEMA     float64
	UpSpeedEMA       float64
	LastTickTime     time.Time
	SkippedFiles     []string                 // Files skipped due to permissions, locks, or non-regular file types
	Interfaces       []netutils.InterfaceInfo // Available network interfaces
	FallbackDeadline time.Time                // Wall-clock deadline for the HTTP fallback timer
	ErrorMsg         string                   // Latest background error displayed cleanly inside the TUI
}

type TickMsg struct{}
type HashingProgressMsg float64
type TorrentLoadedMsg struct {
	Torrent  *torrent.Torrent
	Magnet   string
	SeederIP string
}
type SkippedFilesMsg []string
type ErrorMsg string

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
			if m.shouldConfirmQuit() && !m.ConfirmQuit {
				m.ConfirmQuit = true
				return m, nil
			}
			m.Quitting = true
			return m, tea.Quit
		}
		// Any other key cancels the pending quit confirmation.
		m.ConfirmQuit = false

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
		if msg.SeederIP != "" {
			m.SeederIP = msg.SeederIP
		}
		return m, nil

	case SkippedFilesMsg:
		m.SkippedFiles = append(m.SkippedFiles, msg...)
		return m, nil

	case ErrorMsg:
		m.ErrorMsg = string(msg)
		return m, nil

	case TickMsg:
		if m.Torrent != nil {
			stats := m.Torrent.Stats()
			currentRead := stats.BytesReadUsefulData.Int64()
			currentWrite := stats.BytesWrittenData.Int64()

			if !m.LastTickTime.IsZero() {
				elapsed := time.Since(m.LastTickTime).Seconds()
				if elapsed > 0 {
					currentDownSpeed := float64(currentRead-m.LastBytesRead) / elapsed
					currentUpSpeed := float64(currentWrite-m.LastBytesWritten) / elapsed

					alpha := 0.2
					if m.DownSpeedEMA == 0 {
						m.DownSpeedEMA = currentDownSpeed
					} else {
						m.DownSpeedEMA = m.DownSpeedEMA + alpha*(currentDownSpeed-m.DownSpeedEMA)
					}

					if m.UpSpeedEMA == 0 {
						m.UpSpeedEMA = currentUpSpeed
					} else {
						m.UpSpeedEMA = m.UpSpeedEMA + alpha*(currentUpSpeed-m.UpSpeedEMA)
					}

					m.DownSpeed = int64(m.DownSpeedEMA)
					m.UpSpeed = int64(m.UpSpeedEMA)
				}
			}

			m.LastBytesRead = currentRead
			m.LastBytesWritten = currentWrite
			m.LastTickTime = time.Now()

			if m.Torrent.Info() != nil {
				prog := float64(m.Torrent.BytesCompleted()) / float64(m.Torrent.Length())
				if prog >= 1.0 && m.Torrent.Length() > 0 {
					m.IsComplete = true
				}
			}
		}
		return m, Tick()
	}
	return m, nil
}

func (m Model) shouldConfirmQuit() bool {
	if m.Torrent == nil {
		return false
	}
	stats := m.Torrent.Stats()
	if stats.ActivePeers <= 0 {
		return false
	}
	return m.Mode == "seed" || m.IsComplete
}

func (m Model) View() string {
	if m.Quitting {
		return "\n  Goodbye!\n\n"
	}

	var s strings.Builder

	header := fmt.Sprintf(" Torrent Class - %s ", strings.ToUpper(m.Mode))
	if m.Version != "" && m.Version != "dev" {
		header = fmt.Sprintf(" Torrent Class v%s - %s ", strings.TrimPrefix(m.Version, "v"), strings.ToUpper(m.Mode))
	}
	s.WriteString(titleStyle.Render(header))
	s.WriteString("\n\n")

	ifaceName := ""
	for _, iface := range m.Interfaces {
		if iface.IP == m.IP {
			ifaceName = iface.Name
			break
		}
	}

	ipDisplay := m.IP
	if ifaceName != "" {
		ipDisplay = fmt.Sprintf("%s (%s)", m.IP, ifaceName)
	}

	s.WriteString(infoStyle.Render("Local IP: ") + lipgloss.NewStyle().Foreground(lipgloss.Color("#F43F5E")).Underline(true).Bold(true).Render(ipDisplay))
	s.WriteString("\n")
	s.WriteString(infoStyle.Render(fmt.Sprintf("Port:     %d", m.Port)))
	s.WriteString("\n\n")

	if len(m.Interfaces) > 1 {
		s.WriteString(labelStyle.Render("Detected Adapters:"))
		s.WriteString("\n")
		for _, iface := range m.Interfaces {
			prefix := "  "
			if iface.IP == m.IP {
				prefix = "> "
			}
			s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8")).Render(fmt.Sprintf("%s%s (%s)", prefix, iface.IP, iface.Name)))
			s.WriteString("\n")
		}
		s.WriteString(lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("#64748B")).Render("  Tip: Peers MUST be on the same subnet as your IP"))
		s.WriteString("\n\n")
	}

	if m.ErrorMsg != "" {
		s.WriteString(errorStyle.Render("  ERROR: " + m.ErrorMsg))
		s.WriteString("\n\n")
	}

	if len(m.SkippedFiles) > 0 {
		s.WriteString(errorStyle.Render(fmt.Sprintf("  WARNING: %d item(s) skipped (permissions, lock, or symlink)", len(m.SkippedFiles))))
		s.WriteString("\n\n")
	}

	// Status Line
	s.WriteString(labelStyle.Render("Status:    "))
	if m.IsHashing {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FACC15")).Render("HASHING FILES..."))
	} else if m.Mode == "seed" {
		s.WriteString(successStyle.Render("SEEDING"))
	} else if m.IsComplete {
		s.WriteString(successStyle.Render("✓ COMPLETE — Keep this window open to share with classmates!"))
	} else if m.Torrent == nil {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FACC15")).Render("WAITING FOR MAGNET..."))
		s.WriteString("\n")
		if !m.FallbackDeadline.IsZero() {
			remaining := int(time.Until(m.FallbackDeadline).Seconds())
			if remaining > 0 {
				s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FACC15")).Render(fmt.Sprintf("  %d seconds before HTTP fallback", remaining)))
			} else {
				s.WriteString(lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("#64748B")).Render("  Still listening for UDP broadcasts..."))
			}
		}
	} else {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FACC15")).Render("DOWNLOADING"))
	}
	s.WriteString("\n")

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

	if m.HTTPAddr != "" {
		s.WriteString(labelStyle.Render("Deploy:    "))
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#34D399")).Underline(true).Render(m.HTTPAddr))
		s.WriteString("\n\n")
	}

	if m.Torrent != nil && !m.IsHashing {
		stats := m.Torrent.Stats()

		if m.Torrent.Info() == nil {
			s.WriteString(labelStyle.Render("Metadata:  "))
			s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("#FACC15")).Render("WAITING..."))
			s.WriteString("\n\n")
		} else {
			s.WriteString(labelStyle.Render("Payload:    "))
			s.WriteString(valueStyle.Render(fmt.Sprintf("%s (%s)", m.Torrent.Name(), utils.HumanizeBytes(m.Torrent.Length()))))
			s.WriteString("\n")
			if m.SeederIP != "" && m.Mode == "download" {
				s.WriteString(labelStyle.Render("Source IP:  "))
				s.WriteString(valueStyle.Render(m.SeederIP))
				s.WriteString("\n")
			}

			total := float64(m.Torrent.Length())
			completed := float64(m.Torrent.BytesCompleted())
			prog := 0.0
			if total > 0 {
				prog = completed / total
			}

			s.WriteString(labelStyle.Render("Progress:   "))
			s.WriteString(valueStyle.Render(fmt.Sprintf("%.1f%%", prog*100)))
			s.WriteString("\n")
			s.WriteString("  " + m.Progress.ViewAs(prog))
			s.WriteString("\n\n")
		}

		s.WriteString(labelStyle.Render("Peers:      "))
		s.WriteString(valueStyle.Render(fmt.Sprintf("%d", stats.ActivePeers)))
		s.WriteString("\n\n")

		s.WriteString(labelStyle.Render("Downloaded: "))
		s.WriteString(valueStyle.Render(utils.HumanizeBytes(stats.BytesReadUsefulData.Int64())))
		s.WriteString("\n")
		s.WriteString(labelStyle.Render("Down Speed: "))
		s.WriteString(valueStyle.Render(utils.HumanizeBytes(m.DownSpeed) + "/s"))
		s.WriteString("\n\n")

		s.WriteString(labelStyle.Render("Uploaded:   "))
		s.WriteString(valueStyle.Render(utils.HumanizeBytes(stats.BytesWrittenData.Int64())))
		s.WriteString("\n")
		s.WriteString(labelStyle.Render("Up Speed:   "))
		s.WriteString(valueStyle.Render(utils.HumanizeBytes(m.UpSpeed) + "/s"))
		s.WriteString("\n")
	}

	s.WriteString("\n\n")
	if m.ConfirmQuit && m.Torrent != nil {
		s.WriteString(warnStyle.Render(fmt.Sprintf("  Still sharing with %d peer(s) — press 'q' again to confirm exit", m.Torrent.Stats().ActivePeers)))
	} else {
		s.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("  Press 'q' to quit"))
	}

	return s.String()
}
