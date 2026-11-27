package netutils

import (
	"net"
	"sort"
	"strings"
)

// InterfaceInfo represents details of a network interface.
type InterfaceInfo struct {
	Name   string
	IP     string
	Subnet string
}

var virtualAdapterTokens = []string{
	"vethernet",
	"docker",
	"br-",
	"vmware",
	"virtualbox",
	"vboxnet",
	"virbr",
	"wsl",
	"hyper-v",
	"tailscale",
	"zerotier",
	"utun",
	"hamachi",
	"vpn",
}

// IsVirtualAdapter reports whether an interface name looks like a virtual or tunnel adapter.
func IsVirtualAdapter(name string) bool {
	lower := strings.ToLower(name)
	for _, tok := range virtualAdapterTokens {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	// Exact/prefix matches for tun/tap interfaces (e.g. tun0, tap0) without matching unrelated names.
	return strings.HasPrefix(lower, "tun") || strings.HasPrefix(lower, "tap")
}

// GetLocalIP returns the best local IP for broadcasting.
func GetLocalIP() string {
	ifaces, _ := GetValidInterfaces()
	if len(ifaces) > 0 {
		return ifaces[0].IP
	}
	return "127.0.0.1"
}

// GetValidInterfaces returns a list of valid IPv4 network interfaces and their IPs.
func GetValidInterfaces() ([]InterfaceInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var results []InterfaceInfo
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, address := range addrs {
			ipnet, ok := address.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipnet.IP.To4()
			if ip == nil || ip.IsLinkLocalUnicast() {
				continue
			}

			results = append(results, InterfaceInfo{
				Name:   iface.Name,
				IP:     ip.String(),
				Subnet: ipnet.String(),
			})
		}
	}

	sortInterfaces(results)
	return results, nil
}

// IsPrivateIP checks if an IPv4 address is in an RFC 1918 private range.
func IsPrivateIP(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	return ip4.IsPrivate()
}

func sortInterfaces(ifaces []InterfaceInfo) {
	sort.SliceStable(ifaces, func(i, j int) bool {
		ipI := net.ParseIP(ifaces[i].IP).To4()
		ipJ := net.ParseIP(ifaces[j].IP).To4()

		isPrivI := ipI != nil && ipI.IsPrivate()
		isPrivJ := ipJ != nil && ipJ.IsPrivate()

		if isPrivI != isPrivJ {
			return isPrivI
		}

		isVirtI := IsVirtualAdapter(ifaces[i].Name)
		isVirtJ := IsVirtualAdapter(ifaces[j].Name)

		if isVirtI != isVirtJ {
			return !isVirtI
		}

		return false
	})
}

// GetBroadcastAddr calculates the directed broadcast address for a given IPNet.
func GetBroadcastAddr(ipnet *net.IPNet) string {
	ip := ipnet.IP.To4()
	mask := ipnet.Mask
	if ip == nil || len(mask) != len(ip) {
		return "255.255.255.255"
	}
	broadcast := make(net.IP, len(ip))
	for i := 0; i < len(ip); i++ {
		broadcast[i] = ip[i] | ^mask[i]
	}
	return broadcast.String()
}
