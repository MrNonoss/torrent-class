package discovery

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"torrent-class/internal/netutils"
)

const (
	BroadcastPort = 4243
	MagicPrefix   = "TORRENT_DIST:"
)

// Broadcaster sends the magnet link and local address over UDP broadcast.
// It maintains a persistent connection per local interface to avoid the overhead
// of opening and closing a new socket on every tick.
type Broadcaster struct {
	MagnetLink string
	ListenPort int
	Interval   time.Duration
	connsMu    sync.Mutex
	conns      map[string]*net.UDPConn // local IP -> cached UDP connection
}

func NewBroadcaster(magnetLink string, listenPort int) *Broadcaster {
	return &Broadcaster{
		MagnetLink: magnetLink,
		ListenPort: listenPort,
		Interval:   2 * time.Second,
		conns:      make(map[string]*net.UDPConn),
	}
}

// Start broadcasts immediately and then on each tick until ctx is cancelled.
func (b *Broadcaster) Start(ctx context.Context) error {
	ticker := time.NewTicker(b.Interval)
	defer ticker.Stop()
	defer b.closeConns()

	b.broadcast()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			b.broadcast()
		}
	}
}

func (b *Broadcaster) broadcast() {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return
	}

	for _, address := range addrs {
		ipnet, ok := address.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		ip := ipnet.IP.To4()
		if ip == nil || ip.IsLinkLocalUnicast() {
			continue
		}
		b.sendBroadcastOnIP(ipnet)
	}
}

func (b *Broadcaster) getOrCreateConn(localIP string) (*net.UDPConn, error) {
	b.connsMu.Lock()
	defer b.connsMu.Unlock()

	if conn, ok := b.conns[localIP]; ok {
		return conn, nil
	}

	laddr, _ := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:0", localIP))
	conn, err := net.ListenUDP("udp4", laddr)
	if err != nil {
		conn, err = net.ListenUDP("udp4", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create UDP conn for %s: %w", localIP, err)
		}
	}

	b.conns[localIP] = conn
	return conn, nil
}

func (b *Broadcaster) closeConns() {
	b.connsMu.Lock()
	defer b.connsMu.Unlock()
	for _, conn := range b.conns {
		conn.Close()
	}
	b.conns = make(map[string]*net.UDPConn)
}

func (b *Broadcaster) sendBroadcastOnIP(ipnet *net.IPNet) {
	localIP := ipnet.IP.String()
	message := []byte(fmt.Sprintf("%s%s|%s|%d", MagicPrefix, b.MagnetLink, localIP, b.ListenPort))

	broadcastAddr := netutils.GetBroadcastAddr(ipnet)
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", broadcastAddr, BroadcastPort))
	if err != nil {
		return
	}

	conn, err := b.getOrCreateConn(localIP)
	if err != nil {
		return
	}

	_, _ = conn.WriteToUDP(message, addr)

	limAddr, _ := net.ResolveUDPAddr("udp4", fmt.Sprintf("255.255.255.255:%d", BroadcastPort))
	_, _ = conn.WriteToUDP(message, limAddr)
}

type DiscoveryInfo struct {
	Magnet string
	IP     string
	Port   int
}

// ParseAnnouncement parses a raw UDP discovery payload.
// When sourceIP is a valid routable IPv4 address (i.e. observed directly from ReadFromUDP),
// it takes precedence over the self-reported IP in the payload so peers always dial
// the address the packet actually came from.
func ParseAnnouncement(data []byte, sourceIP string) (DiscoveryInfo, bool) {
	msg := string(data)
	if !strings.HasPrefix(msg, MagicPrefix) {
		return DiscoveryInfo{}, false
	}

	parts := strings.Split(msg[len(MagicPrefix):], "|")
	if len(parts) != 3 {
		return DiscoveryInfo{}, false
	}

	magnet := parts[0]
	if magnet == "" {
		return DiscoveryInfo{}, false
	}

	port, err := strconv.Atoi(parts[2])
	if err != nil || port <= 0 || port > 65535 {
		return DiscoveryInfo{}, false
	}

	peerIP := parts[1]
	if isUsablePeerIP(sourceIP) {
		peerIP = sourceIP
	} else if !isUsablePeerIP(peerIP) {
		return DiscoveryInfo{}, false
	}

	return DiscoveryInfo{
		Magnet: magnet,
		IP:     peerIP,
		Port:   port,
	}, true
}

func isUsablePeerIP(raw string) bool {
	if raw == "" {
		return false
	}
	parsed := net.ParseIP(raw)
	if parsed == nil {
		return false
	}
	ip4 := parsed.To4()
	if ip4 == nil || ip4.IsLinkLocalUnicast() || ip4.IsUnspecified() {
		return false
	}
	return true
}

// Listener listens for magnet links over UDP broadcast.
type Listener struct {
	Foundchan chan DiscoveryInfo
}

func NewListener() *Listener {
	return &Listener{
		Foundchan: make(chan DiscoveryInfo, 500),
	}
}

// Listen blocks until either an error occurs or ctx is cancelled.
func (l *Listener) Listen(ctx context.Context) error {
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", BroadcastPort))
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	buf := make([]byte, 2048)
	for {
		n, srcAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}

		srcIP := ""
		if srcAddr != nil && srcAddr.IP != nil {
			srcIP = srcAddr.IP.String()
		}

		info, ok := ParseAnnouncement(buf[:n], srcIP)
		if !ok {
			continue
		}

		select {
		case l.Foundchan <- info:
		default:
		}
	}
}
