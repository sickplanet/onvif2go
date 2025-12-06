package onvif

import (
	"encoding/xml"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	discoveryPort   = 3702
	discoveryAddr   = "239.255.255.250:3702"
	discoveryProbe  = `<?xml version="1.0" encoding="UTF-8"?>
<soap:Envelope xmlns:soap="http://www.w3.org/2003/05/soap-envelope" xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:tns="http://schemas.xmlsoap.org/ws/2005/04/discovery" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery">
	<soap:Header>
		<wsa:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</wsa:Action>
		<wsa:MessageID>urn:uuid:%s</wsa:MessageID>
		<wsa:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</wsa:To>
	</soap:Header>
	<soap:Body>
		<tns:Probe>
			<tns:Types>tdn:NetworkVideoTransmitter</tns:Types>
		</tns:Probe>
	</soap:Body>
</soap:Envelope>`
)

// DiscoveredDevice represents a discovered ONVIF device
type DiscoveredDevice struct {
	Endpoint string `json:"endpoint"`
	Name     string `json:"name"`
	Address  string `json:"address"`
	Types    string `json:"types"`
	Scopes   string `json:"scopes"`
}

// ProbeMatchResponse represents the WS-Discovery response
type ProbeMatchResponse struct {
	XMLName xml.Name `xml:"Envelope"`
	Body    struct {
		ProbeMatches struct {
			ProbeMatch []struct {
				EndpointReference struct {
					Address string `xml:"Address"`
				} `xml:"EndpointReference"`
				Types  string `xml:"Types"`
				Scopes string `xml:"Scopes"`
				XAddrs string `xml:"XAddrs"`
			} `xml:"ProbeMatch"`
		} `xml:"ProbeMatches"`
	} `xml:"Body"`
}

// generateUUID generates a simple UUID for the discovery message
func generateUUID() string {
	timestamp := time.Now().UnixNano()
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		timestamp&0xffffffff,
		(timestamp>>32)&0xffff,
		0x4000|(timestamp>>48)&0x0fff,
		0x8000|(timestamp>>60)&0x3fff,
		timestamp&0xffffffffffff,
	)
}

// parseDeviceName extracts the device name from scopes
func parseDeviceName(scopes string) string {
	// Try to extract name from scopes
	for _, scope := range strings.Split(scopes, " ") {
		if strings.Contains(scope, "name/") {
			parts := strings.Split(scope, "name/")
			if len(parts) > 1 {
				return strings.ReplaceAll(parts[1], "%20", " ")
			}
		}
		if strings.Contains(scope, "hardware/") {
			parts := strings.Split(scope, "hardware/")
			if len(parts) > 1 {
				return strings.ReplaceAll(parts[1], "%20", " ")
			}
		}
	}
	return "Unknown Device"
}

// Discover finds ONVIF devices on the network
func Discover(timeout time.Duration) ([]DiscoveredDevice, error) {
	devices := make([]DiscoveredDevice, 0)
	seen := make(map[string]bool)
	var mu sync.Mutex

	// Get all network interfaces
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to get network interfaces: %w", err)
	}

	var wg sync.WaitGroup

	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			ip := ipNet.IP.To4()
			if ip == nil {
				continue
			}

			wg.Add(1)
			go func(ip net.IP) {
				defer wg.Done()

				localAddr := &net.UDPAddr{
					IP:   ip,
					Port: 0,
				}

				conn, err := net.ListenUDP("udp4", localAddr)
				if err != nil {
					return
				}
				defer conn.Close()

				// Set deadline for responses
				if deadlineErr := conn.SetDeadline(time.Now().Add(timeout)); deadlineErr != nil {
					return
				}

				// Send discovery probe
				multicastAddr, err := net.ResolveUDPAddr("udp4", discoveryAddr)
				if err != nil {
					return
				}

				probeMessage := fmt.Sprintf(discoveryProbe, generateUUID())
				_, err = conn.WriteToUDP([]byte(probeMessage), multicastAddr)
				if err != nil {
					return
				}

				// Receive responses
				buffer := make([]byte, 8192)
				for {
					n, remoteAddr, err := conn.ReadFromUDP(buffer)
					if err != nil {
						break
					}

					var response ProbeMatchResponse
					if err := xml.Unmarshal(buffer[:n], &response); err != nil {
						continue
					}

					for _, match := range response.Body.ProbeMatches.ProbeMatch {
						endpoints := strings.Split(match.XAddrs, " ")
						for _, endpoint := range endpoints {
							endpoint = strings.TrimSpace(endpoint)
							if endpoint == "" {
								continue
							}

							mu.Lock()
							if !seen[endpoint] {
								seen[endpoint] = true
								devices = append(devices, DiscoveredDevice{
									Endpoint: endpoint,
									Name:     parseDeviceName(match.Scopes),
									Address:  remoteAddr.IP.String(),
									Types:    match.Types,
									Scopes:   match.Scopes,
								})
							}
							mu.Unlock()
						}
					}
				}
			}(ip)
		}
	}

	wg.Wait()
	return devices, nil
}

// DiscoverOnInterface discovers devices on a specific network interface
func DiscoverOnInterface(ifaceName string, timeout time.Duration) ([]DiscoveredDevice, error) {
	devices := make([]DiscoveredDevice, 0)
	seen := make(map[string]bool)

	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return nil, fmt.Errorf("interface not found: %w", err)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return nil, fmt.Errorf("failed to get interface addresses: %w", err)
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		ip := ipNet.IP.To4()
		if ip == nil {
			continue
		}

		localAddr := &net.UDPAddr{
			IP:   ip,
			Port: 0,
		}

		conn, err := net.ListenUDP("udp4", localAddr)
		if err != nil {
			continue
		}
		defer conn.Close()

		// Set deadline for responses
		if deadlineErr := conn.SetDeadline(time.Now().Add(timeout)); deadlineErr != nil {
			continue
		}

		// Send discovery probe
		multicastAddr, err := net.ResolveUDPAddr("udp4", discoveryAddr)
		if err != nil {
			continue
		}

		probeMessage := fmt.Sprintf(discoveryProbe, generateUUID())
		_, err = conn.WriteToUDP([]byte(probeMessage), multicastAddr)
		if err != nil {
			continue
		}

		// Receive responses
		buffer := make([]byte, 8192)
		for {
			n, remoteAddr, err := conn.ReadFromUDP(buffer)
			if err != nil {
				break
			}

			var response ProbeMatchResponse
			if err := xml.Unmarshal(buffer[:n], &response); err != nil {
				continue
			}

			for _, match := range response.Body.ProbeMatches.ProbeMatch {
				endpoints := strings.Split(match.XAddrs, " ")
				for _, endpoint := range endpoints {
					endpoint = strings.TrimSpace(endpoint)
					if endpoint == "" {
						continue
					}

					if !seen[endpoint] {
						seen[endpoint] = true
						devices = append(devices, DiscoveredDevice{
							Endpoint: endpoint,
							Name:     parseDeviceName(match.Scopes),
							Address:  remoteAddr.IP.String(),
							Types:    match.Types,
							Scopes:   match.Scopes,
						})
					}
				}
			}
		}
	}

	return devices, nil
}
