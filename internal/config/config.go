package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/chhbzhou/ils-gateway/internal/provider/applewloc"
)

type Config struct {
	Enabled          bool
	SocketPath       string
	MaxConnections   int
	MaxBodyBytes     int64
	MaxBufferedBytes int64
	Profile          applewloc.Profile
	ProfileSet       bool
	profileLatSet    bool
	profileLonSet    bool
	ListenAddr       string
	ListenPort       int
	ProxyPort        int
	UpstreamSocks5   string
	DeviceMAC        string
	DeviceMACs       []string
	DeviceIPv4       string
	DeviceIPv6       string
	DeviceIPs        []string
	IPv6Block        bool
	WLocHosts        []string
}

type parsedProfile struct {
	section string
	profile applewloc.Profile
	active  bool
	latSet  bool
	lonSet  bool
}

type parsedDevice struct {
	enabled bool
	mac     string
	ips     []string
}

func Default() Config {
	return Config{SocketPath: "/var/run/locspoofd/control.sock", MaxConnections: 32, MaxBodyBytes: 1 << 20, MaxBufferedBytes: 16 << 20, ListenAddr: "0.0.0.0", ListenPort: 10443, ProxyPort: 10445}
}

// ParseUCI accepts the small key/value form used by unit tests and the UCI
// option form emitted by OpenWrt (/etc/config/ios-location-spoofer).
func ParseUCI(data []byte) (Config, error) {
	c := Default()
	section := ""
	sectionType := ""
	uciMode := false
	profiles := make([]parsedProfile, 0)
	devices := make([]parsedDevice, 0)
	var currentProfile *parsedProfile
	var currentDevice *parsedDevice
	finishSection := func() {
		if currentProfile != nil {
			profiles = append(profiles, *currentProfile)
			currentProfile = nil
		}
		if currentDevice != nil {
			devices = append(devices, *currentDevice)
			currentDevice = nil
		}
	}
	for n, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.SplitN(raw, "#", 2)[0])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "config ") {
			finishSection()
			uciMode = true
			parts := strings.Fields(line)
			section = ""
			sectionType = ""
			if len(parts) >= 2 {
				sectionType = parts[1]
			}
			if len(parts) >= 3 {
				section = strings.Trim(parts[2], "'\"")
			}
			switch sectionType {
			case "profile":
				currentProfile = &parsedProfile{section: section}
			case "device":
				currentDevice = &parsedDevice{enabled: true}
			}
			continue
		}
		if strings.HasPrefix(line, "list ") {
			parts := strings.Fields(line)
			if len(parts) >= 3 && sectionType == "ios_location_spoofer" && section == "main" {
				value := strings.Trim(parts[2], "'\"")
				switch parts[1] {
				case "wloc_host":
					c.WLocHosts = append(c.WLocHosts, value)
				case "device_macs":
					c.DeviceMACs = append(c.DeviceMACs, value)
				case "device_ips":
					c.DeviceIPs = append(c.DeviceIPs, value)
				}
			} else if len(parts) >= 3 && sectionType == "device" && currentDevice != nil && parts[1] == "ips" {
				currentDevice.ips = append(currentDevice.ips, strings.Trim(parts[2], "'\""))
			}
			continue
		}
		var k, v string
		if strings.HasPrefix(line, "option ") {
			parts := strings.Fields(line)
			if len(parts) < 3 {
				return c, fmt.Errorf("line %d: malformed UCI option", n+1)
			}
			k, v = parts[1], strings.Trim(parts[2], "'\"")
		} else {
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				return c, fmt.Errorf("line %d: expected key=value or UCI option", n+1)
			}
			k, v = strings.TrimSpace(parts[0]), strings.Trim(strings.TrimSpace(parts[1]), "'\"")
		}
		var err error
		switch {
		case uciMode && sectionType == "profile" && currentProfile != nil:
			switch k {
			case "active":
				currentProfile.active, err = strconv.ParseBool(v)
			case "latitude", "longitude", "altitude", "horizontal_accuracy", "vertical_accuracy", "random_radius":
				if v == "" {
					continue
				}
				var value float64
				value, err = strconv.ParseFloat(v, 64)
				if err == nil {
					switch k {
					case "latitude":
						currentProfile.profile.Latitude, currentProfile.latSet = value, true
					case "longitude":
						currentProfile.profile.Longitude, currentProfile.lonSet = value, true
					case "altitude":
						currentProfile.profile.Altitude = value
					case "horizontal_accuracy":
						currentProfile.profile.HorizontalAccuracy = value
					case "vertical_accuracy":
						currentProfile.profile.VerticalAccuracy = value
					case "random_radius":
						currentProfile.profile.RandomRadius = value
					}
				}
			}
		case uciMode && sectionType == "device" && currentDevice != nil:
			switch k {
			case "enabled":
				currentDevice.enabled, err = strconv.ParseBool(v)
			case "mac":
				currentDevice.mac = v
			case "ip":
				if v != "" {
					currentDevice.ips = append(currentDevice.ips, v)
				}
			}
		case uciMode && !(sectionType == "ios_location_spoofer" && section == "main"):
			continue
		default:
			switch k {
			case "enabled":
				c.Enabled, err = strconv.ParseBool(v)
			case "socket_path":
				c.SocketPath = v
			case "max_connections":
				c.MaxConnections, err = strconv.Atoi(v)
			case "max_body_bytes":
				c.MaxBodyBytes, err = strconv.ParseInt(v, 10, 64)
			case "max_buffered_bytes":
				c.MaxBufferedBytes, err = strconv.ParseInt(v, 10, 64)
			case "listen_addr":
				c.ListenAddr = v
			case "listen_port":
				c.ListenPort, err = strconv.Atoi(v)
			case "proxy_port":
				c.ProxyPort, err = strconv.Atoi(v)
			case "upstream_socks5":
				c.UpstreamSocks5 = v
			case "device_mac":
				c.DeviceMAC = v
			case "device_ip":
				c.DeviceIPv4 = v
			case "device_ipv4":
				c.DeviceIPv4 = v
			case "device_ipv6":
				c.DeviceIPv6 = v
			case "ipv6_block":
				c.IPv6Block, err = strconv.ParseBool(v)
			}
		}
		if err != nil {
			return c, fmt.Errorf("line %d: %w", n+1, err)
		}
	}
	finishSection()
	for _, device := range devices {
		if !device.enabled {
			continue
		}
		if device.mac != "" {
			c.DeviceMACs = append(c.DeviceMACs, device.mac)
		}
		c.DeviceIPs = append(c.DeviceIPs, device.ips...)
	}
	activeProfiles := 0
	validProfiles := 0
	legacyProfile := -1
	for index, profile := range profiles {
		if profile.latSet != profile.lonSet {
			return c, fmt.Errorf("profile requires latitude and longitude")
		}
		if profile.latSet {
			if err := profile.profile.Validate(); err != nil {
				return c, err
			}
			validProfiles++
			if profile.section == "location" {
				legacyProfile = index
			}
		}
		if profile.active {
			if !profile.latSet {
				return c, fmt.Errorf("active profile requires latitude and longitude")
			}
			activeProfiles++
			c.Profile, c.ProfileSet = profile.profile, true
			c.profileLatSet, c.profileLonSet = true, true
		}
	}
	if activeProfiles > 1 {
		return c, fmt.Errorf("multiple active profiles")
	}
	if activeProfiles == 0 {
		selected := -1
		if validProfiles == 1 {
			for index, profile := range profiles {
				if profile.latSet {
					selected = index
					break
				}
			}
		} else if legacyProfile >= 0 {
			selected = legacyProfile
		}
		if selected >= 0 {
			c.Profile, c.ProfileSet = profiles[selected].profile, true
			c.profileLatSet, c.profileLonSet = true, true
		}
	}
	return c, c.Validate()
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return ParseUCI(b)
}

func (c Config) Validate() error {
	if c.SocketPath == "" || len(c.SocketPath) > 100 {
		return fmt.Errorf("invalid socket_path")
	}
	if c.ListenAddr != "0.0.0.0" {
		return fmt.Errorf("listen_addr is fixed at 0.0.0.0")
	}
	if c.ListenPort != 10443 || c.ProxyPort != 10445 {
		return fmt.Errorf("listen_port/proxy_port are fixed at 10443/10445")
	}
	if c.ListenAddr == "" || net.ParseIP(c.ListenAddr) == nil {
		return fmt.Errorf("invalid listen_addr")
	}
	validateMAC := func(value, field string) error {
		mac, err := net.ParseMAC(value)
		if err != nil || len(mac) != 6 {
			return fmt.Errorf("invalid %s", field)
		}
		return nil
	}
	if c.DeviceMAC != "" {
		if err := validateMAC(c.DeviceMAC, "device_mac"); err != nil {
			return err
		}
	}
	macSeen := make(map[string]struct{})
	if c.DeviceMAC != "" {
		m, _ := net.ParseMAC(c.DeviceMAC)
		macSeen[strings.ToLower(m.String())] = struct{}{}
	}
	for _, mac := range c.DeviceMACs {
		if err := validateMAC(mac, "device_macs"); err != nil {
			return err
		}
		m, _ := net.ParseMAC(mac)
		key := strings.ToLower(m.String())
		if _, ok := macSeen[key]; ok {
			return fmt.Errorf("duplicate device MAC")
		}
		macSeen[key] = struct{}{}
	}
	ipSeen := make(map[string]struct{})
	if c.DeviceIPv4 != "" {
		if ip := net.ParseIP(c.DeviceIPv4); ip == nil || ip.To4() == nil {
			return fmt.Errorf("invalid device_ipv4")
		}
		ipSeen[net.ParseIP(c.DeviceIPv4).To4().String()] = struct{}{}
	}
	if c.DeviceIPv6 != "" {
		if ip := net.ParseIP(c.DeviceIPv6); ip == nil || ip.To4() != nil {
			return fmt.Errorf("invalid device_ipv6")
		}
		ipSeen[net.ParseIP(c.DeviceIPv6).String()] = struct{}{}
	}
	for _, value := range c.DeviceIPs {
		ip := net.ParseIP(value)
		if ip == nil {
			return fmt.Errorf("invalid device_ips")
		}
		key := ip.String()
		if _, ok := ipSeen[key]; ok {
			return fmt.Errorf("duplicate device IP")
		}
		ipSeen[key] = struct{}{}
	}
	if c.Enabled && len(macSeen) == 0 && len(ipSeen) == 0 {
		return fmt.Errorf("device MAC or IP is required when enabled")
	}
	for _, h := range c.WLocHosts {
		if strings.ContainsAny(h, " /\t") || !strings.Contains(h, ".") {
			return fmt.Errorf("invalid wloc_host")
		}
		switch strings.ToLower(strings.TrimSuffix(h, ".")) {
		case "gsp-ssl.ls.apple.com", "gspe1-ssl.ls.apple.com", "gs-loc.apple.com", "gs-loc-cn.apple.com", "bluedot.is.autonavi.com", "bluedot.is.autonavi.com.gds.alibabadns.com":
		default:
			return fmt.Errorf("wloc_host is outside compiled leaf allowlist")
		}
	}
	if c.UpstreamSocks5 != "" {
		host, port, err := net.SplitHostPort(c.UpstreamSocks5)
		p, portErr := strconv.Atoi(port)
		if err != nil || host == "" || portErr != nil || p < 1 || p > 65535 {
			return fmt.Errorf("invalid upstream_socks5")
		}
	}
	if c.MaxConnections < 1 || c.MaxConnections > 1024 {
		return fmt.Errorf("invalid max_connections")
	}
	if c.MaxBodyBytes < 1024 || c.MaxBodyBytes > 64<<20 {
		return fmt.Errorf("invalid max_body_bytes")
	}
	// A rewrite may simultaneously retain compressed, decoded, rewritten and
	// re-compressed bodies. Reserve four body limits as a hard worst-case.
	// Bounded reads retain one probe byte in addition to the configured body
	// limit. Reserve four worst-case body representations plus that probe.
	if c.MaxBufferedBytes < 4*(c.MaxBodyBytes+1) || c.MaxBufferedBytes > 256<<20 {
		return fmt.Errorf("invalid max_buffered_bytes")
	}
	if c.ProfileSet {
		if !c.profileLatSet || !c.profileLonSet {
			return fmt.Errorf("profile requires latitude and longitude")
		}
		if err := c.Profile.Validate(); err != nil {
			return err
		}
	}
	return nil
}
