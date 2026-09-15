package discovery

import (
	"context"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"torrent-class/pkg/netutils"
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
	conns      map[string]*net.UDPConn // local IP → cached UDP connection
}

func NewBroadcaster(magnetLink string, listenPort int) *Broadcaster {
	return &Broadcaster{
		MagnetLink: magnetLink,
		ListenPort: listenPort,
		Interval:   2 * time.Second,
		conns:      make(map[string]*net.UDPConn),
	}
}

// Start broadcasts until the context is cancelled. It closes all cached
// connections on exit so the OS can reclaim the sockets immediately.
func (b *Broadcaster) Start(ctx context.Context) error {
	ticker := time.NewTicker(b.Interval)
	defer ticker.Stop()
	defer b.closeConns()

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
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			ip := ipnet.IP.To4()
			if ip == nil {
				continue
			}

			// Skip APIPA (169.254.x.x)
			if ip[0] == 169 && ip[1] == 254 {
				continue
			}

			// Broadcast on this interface
			b.sendBroadcastOnIP(ipnet)
		}
	}
}

// getOrCreateConn returns a cached UDP connection bound to localIP, creating
// one if it does not yet exist. Reusing connections avoids the kernel overhead
// of a full socket open/close cycle on every broadcast tick (Issue #2).
func (b *Broadcaster) getOrCreateConn(localIP string) (*net.UDPConn, error) {
	b.connsMu.Lock()
	defer b.connsMu.Unlock()

	if conn, ok := b.conns[localIP]; ok {
		return conn, nil
	}

	laddr, _ := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:0", localIP))
	conn, err := net.ListenUDP("udp4", laddr)
	if err != nil {
		// Fallback: bind without a specific local address
		conn, err = net.ListenUDP("udp4", nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create UDP conn for %s: %w", localIP, err)
		}
	}

	b.conns[localIP] = conn
	return conn, nil
}

// closeConns closes all cached UDP connections and resets the map.
func (b *Broadcaster) closeConns() {
	b.connsMu.Lock()
	defer b.connsMu.Unlock()
	for _, conn := range b.conns {
		conn.Close()
	}
	b.conns = make(map[string]*net.UDPConn)
}

func (b *Broadcaster) sendBroadcastOnIP(ipnet *net.IPNet) {
	// Create a message specifically for this interface
	localIP := ipnet.IP.String()
	message := []byte(fmt.Sprintf("%s%s|%s|%d", MagicPrefix, b.MagnetLink, localIP, b.ListenPort))

	// Resolve the broadcast address for this subnet
	broadcastAddr := netutils.GetBroadcastAddr(ipnet)
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", broadcastAddr, BroadcastPort))
	if err != nil {
		log.Printf("UDP Discovery Error: Failed to resolve broadcast address: %v", err)
		return
	}

	// Reuse the persistent connection bound to this interface (Issue #2)
	conn, err := b.getOrCreateConn(localIP)
	if err != nil {
		log.Printf("UDP Discovery Error: %v", err)
		return
	}

	_, _ = conn.WriteToUDP(message, addr)

	// Also send to limited broadcast
	limAddr, _ := net.ResolveUDPAddr("udp4", fmt.Sprintf("255.255.255.255:%d", BroadcastPort))
	_, _ = conn.WriteToUDP(message, limAddr)
}

type DiscoveryInfo struct {
	Magnet string
	IP     string
	Port   int
}

// Listener listens for magnet links over UDP broadcast.
// The channel buffer is large enough to absorb simultaneous bursts
// from a full classroom (Issue #4).
type Listener struct {
	Foundchan chan DiscoveryInfo
}

func NewListener() *Listener {
	return &Listener{
		Foundchan: make(chan DiscoveryInfo, 500),
	}
}

// Listen blocks until either an error occurs or the context is cancelled.
// Cancelling the context causes the blocking ReadFromUDP call to unblock
// immediately, allowing the goroutine to exit cleanly (Issues #6, #10).
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

	// Closing the connection from this goroutine unblocks ReadFromUDP below.
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	buf := make([]byte, 2048)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil // context cancelled — clean exit
			}
			return err
		}

		msg := string(buf[:n])
		if len(msg) > len(MagicPrefix) && msg[:len(MagicPrefix)] == MagicPrefix {
			content := msg[len(MagicPrefix):]
			parts := strings.Split(content, "|")
			if len(parts) == 3 {
				magnet := parts[0]
				ipStr := parts[1]
				port := 0
				fmt.Sscanf(parts[2], "%d", &port)

				// Validate IP: Ignore APIPA addresses from peers
				parsedIP := net.ParseIP(ipStr)
				if parsedIP != nil {
					ip4 := parsedIP.To4()
					if ip4 != nil && ip4[0] == 169 && ip4[1] == 254 {
						// Ignore link-local/APIPA addresses
						continue
					}
				}

				info := DiscoveryInfo{
					Magnet: magnet,
					IP:     ipStr,
					Port:   port,
				}

				select {
				case l.Foundchan <- info:
				default:
					// Already found or channel full
				}
			}
		}
	}
}
