package netutils

import (
	"net"
)

// InterfaceInfo represents details of a network interface
type InterfaceInfo struct {
	Name   string
	IP     string
	Subnet string
}

// GetLocalIP returns the best local IP for broadcasting
func GetLocalIP() string {
	ifaces, _ := GetValidInterfaces()
	if len(ifaces) > 0 {
		return ifaces[0].IP
	}
	return "127.0.0.1"
}

// GetValidInterfaces returns a list of valid network interfaces and their IPs
func GetValidInterfaces() ([]InterfaceInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var results []InterfaceInfo
	for _, iface := range ifaces {
		// Skip loopback and down interfaces
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, address := range addrs {
			if ipnet, ok := address.(*net.IPNet); ok {
				ip := ipnet.IP.To4()
				if ip == nil {
					continue
				}

				// Skip APIPA (169.254.x.x)
				if ip[0] == 169 && ip[1] == 254 {
					continue
				}

				results = append(results, InterfaceInfo{
					Name:   iface.Name,
					IP:     ip.String(),
					Subnet: ipnet.String(),
				})
			}
		}
	}

	// If we're on a private network, move those to the front
	sortInterfaces(results)

	return results, nil
}

// IsPrivateIP checks if an IP is in a private range
func IsPrivateIP(ip net.IP) bool {
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

func sortInterfaces(ifaces []InterfaceInfo) {
	for i := 0; i < len(ifaces); i++ {
		ip := net.ParseIP(ifaces[i].IP)
		if ip != nil && IsPrivateIP(ip.To4()) {
			// Swap to front
			ifaces[0], ifaces[i] = ifaces[i], ifaces[0]
			break
		}
	}
}

// GetBroadcastAddr calculates the broadcast address for a given IPNet
func GetBroadcastAddr(ipnet *net.IPNet) string {
	ip := ipnet.IP.To4()
	mask := ipnet.Mask
	broadcast := make(net.IP, len(ip))
	for i := 0; i < len(ip); i++ {
		broadcast[i] = ip[i] | ^mask[i]
	}
	return broadcast.String()
}
