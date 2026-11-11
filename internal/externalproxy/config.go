package externalproxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

type Spec struct {
	ID         int
	Tag        string
	Protocol   string
	Host       string
	Port       int
	Username   string
	Password   string
	ServerName string
	Transport  string
	Enable     bool

	MieruMultiplexing  string
	MieruHandshakeMode string
	MieruMtu           int
}

var (
	mieruMultiplexingLevels = map[string]bool{"MULTIPLEXING_OFF": true, "MULTIPLEXING_LOW": true, "MULTIPLEXING_MIDDLE": true, "MULTIPLEXING_HIGH": true}
	mieruHandshakeModes     = map[string]bool{"HANDSHAKE_STANDARD": true, "HANDSHAKE_NO_WAIT": true}
)

var tagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func validHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, ch := range label {
			if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-", ch) {
				return false
			}
		}
	}
	return true
}

func (s Spec) Validate() error {
	if s.Protocol != "naive" && s.Protocol != "mieru" && s.Protocol != "trusttunnel" {
		return errors.New("unsupported protocol")
	}
	if !tagPattern.MatchString(s.Tag) {
		return errors.New("invalid outbound tag")
	}
	if !validHost(s.Host) || s.ServerName != "" && !validHost(s.ServerName) {
		return errors.New("invalid server host")
	}
	if s.Port < 1 || s.Port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	if strings.IndexFunc(s.Username+s.Password, unicode.IsControl) >= 0 {
		return errors.New("credentials contain control characters")
	}
	if s.Username == "" || s.Password == "" || len(s.Username) > 255 || len(s.Password) > 1024 {
		return errors.New("username and password are required (maximum 255 and 1024 bytes)")
	}
	if s.Transport != "" && s.Transport != "tcp" && s.Transport != "quic" {
		return errors.New("transport must be tcp or quic")
	}
	if s.Protocol == "mieru" && s.Transport == "quic" {
		return errors.New("mieru transport must be tcp")
	}
	if err := s.validateMieruTuning(); err != nil {
		return err
	}
	if s.Protocol == "naive" && s.ServerName != "" && s.ServerName != s.Host && net.ParseIP(s.Host) == nil {
		return errors.New("NaiveProxy server-name override requires an IP server address")
	}
	return nil
}

func (s Spec) validateMieruTuning() error {
	if s.Protocol != "mieru" {
		if s.MieruMultiplexing != "" || s.MieruHandshakeMode != "" || s.MieruMtu != 0 {
			return errors.New("mieru settings apply only to mieru outbounds")
		}
		return nil
	}
	if s.MieruMultiplexing != "" && !mieruMultiplexingLevels[s.MieruMultiplexing] {
		return errors.New("invalid mieru multiplexing level")
	}
	if s.MieruHandshakeMode != "" && !mieruHandshakeModes[s.MieruHandshakeMode] {
		return errors.New("invalid mieru handshake mode")
	}
	if s.MieruMtu != 0 && (s.MieruMtu < 1280 || s.MieruMtu > 1400) {
		return errors.New("mieru MTU must be between 1280 and 1400")
	}
	return nil
}

func (s Spec) Render(port int) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if port < 1025 || port > 65535 {
		return nil, errors.New("invalid local SOCKS port")
	}
	listen := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	switch s.Protocol {
	case "naive":
		scheme, host := "https", s.Host
		if s.Transport == "quic" {
			scheme = "quic"
		}
		if s.ServerName != "" {
			host = s.ServerName
		}
		remote := url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(s.Port)), User: url.UserPassword(s.Username, s.Password)}
		cfg := map[string]string{"listen": "socks://" + listen, "proxy": remote.String()}
		if host != s.Host {
			cfg["host-resolver-rules"] = "MAP " + host + " " + s.Host
		}
		return json.Marshal(cfg)
	case "mieru":
		server := map[string]any{"portBindings": []any{map[string]any{"port": s.Port, "protocol": "TCP"}}}
		if net.ParseIP(s.Host) == nil {
			server["domainName"] = s.Host
		} else {
			server["ipAddress"] = s.Host
		}
		profile := map[string]any{"profileName": "exit", "user": map[string]string{"name": s.Username, "password": s.Password}, "servers": []any{server}}
		// Unset fields stay absent so the client keeps its own defaults.
		if s.MieruMultiplexing != "" {
			profile["multiplexing"] = map[string]string{"level": s.MieruMultiplexing}
		}
		if s.MieruHandshakeMode != "" {
			profile["handshakeMode"] = s.MieruHandshakeMode
		}
		if s.MieruMtu != 0 {
			profile["mtu"] = s.MieruMtu
		}
		return json.Marshal(map[string]any{
			"profiles":      []any{profile},
			"activeProfile": "exit", "rpcPort": 0, "socks5Port": port, "socks5ListenLAN": false, "loggingLevel": "INFO",
		})
	default:
		name, protocol := s.ServerName, "http2"
		if name == "" {
			name = s.Host
		}
		if s.Transport == "quic" {
			protocol = "http3"
		}
		return []byte(fmt.Sprintf("loglevel = \"info\"\nvpn_mode = \"general\"\nkillswitch_enabled = true\npost_quantum_group_enabled = true\nexclusions = []\n[endpoint]\nhostname = %q\naddresses = [%q]\nhas_ipv6 = true\nusername = %q\npassword = %q\nskip_verification = false\nupstream_protocol = %q\n[listener.socks]\naddress = %q\n", name, net.JoinHostPort(s.Host, strconv.Itoa(s.Port)), s.Username, s.Password, protocol, listen)), nil
	}
}
