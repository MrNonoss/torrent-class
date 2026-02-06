package discovery

import (
	"fmt"
	"net"
	"strings"
	"time"
)

const (
	BroadcastPort = 4243
	MagicPrefix   = "TORRENT_DIST:"
)

// Broadcaster sends the magnet link and local address over UDP broadcast
type Broadcaster struct {
	MagnetLink string
	ListenPort int
	Interval   time.Duration
	stop       chan struct{}
}

func NewBroadcaster(magnetLink string, listenPort int) *Broadcaster {
	return &Broadcaster{
		MagnetLink: magnetLink,
		ListenPort: listenPort,
		Interval:   2 * time.Second,
		stop:       make(chan struct{}),
	}
}

func (b *Broadcaster) Start() error {
	ticker := time.NewTicker(b.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-b.stop:
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

func (b *Broadcaster) sendBroadcastOnIP(ipnet *net.IPNet) {
	// Create a message specifically for this interface
	localIP := ipnet.IP.String()
	message := []byte(fmt.Sprintf("%s%s|%s|%d", MagicPrefix, b.MagnetLink, localIP, b.ListenPort))

	// Resolve the broadcast address for this subnet
	broadcastAddr := getBroadcastAddr(ipnet)
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", broadcastAddr, BroadcastPort))
	if err != nil {
		return
	}

	// We create a new connection for each broadcast to ensure it's sent from the right interface
	// Bind to the local IP to force it out of that specific interface
	laddr, _ := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:0", localIP))
	conn, err := net.ListenUDP("udp4", laddr)
	if err != nil {
		// Fallback: try without binding
		conn, err = net.ListenUDP("udp4", nil)
		if err != nil {
			return
		}
	}
	defer conn.Close()

	_, _ = conn.WriteToUDP(message, addr)

	// Also send to limited broadcast
	limAddr, _ := net.ResolveUDPAddr("udp4", fmt.Sprintf("255.255.255.255:%d", BroadcastPort))
	_, _ = conn.WriteToUDP(message, limAddr)
}

func getBroadcastAddr(ipnet *net.IPNet) string {
	ip := ipnet.IP.To4()
	mask := ipnet.Mask
	broadcast := make(net.IP, len(ip))
	for i := 0; i < len(ip); i++ {
		broadcast[i] = ip[i] | ^mask[i]
	}
	return broadcast.String()
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}

	var bestIP string
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

			// If it's a private network address, it's a very good candidate
			if isPrivateIP(ip) {
				return ip.String()
			}

			bestIP = ip.String()
		}
	}

	if bestIP != "" {
		return bestIP
	}
	return "127.0.0.1"
}

func isPrivateIP(ip net.IP) bool {
	if ip[0] == 10 {
		return true
	}
	if ip[0] == 172 && (ip[1] >= 16 && ip[1] <= 31) {
		return true
	}
	if ip[0] == 192 && ip[1] == 168 {
		return true
	}
	return false
}

func (b *Broadcaster) Stop() {
	close(b.stop)
}

type DiscoveryInfo struct {
	Magnet string
	IP     string
	Port   int
}

// Listener listens for magnet links over UDP broadcast
type Listener struct {
	Foundchan chan DiscoveryInfo
}

func NewListener() *Listener {
	return &Listener{
		Foundchan: make(chan DiscoveryInfo, 1),
	}
}

func (l *Listener) Listen() error {
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf(":%d", BroadcastPort))
	if err != nil {
		return err
	}

	conn, err := net.ListenUDP("udp4", addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	buf := make([]byte, 2048)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
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
