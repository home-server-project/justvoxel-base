package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"

	"github.com/home-server-project/justvoxel-webui/internal/api"
)

// Only connection presentation data reaches this page, never provider auth data.
type connectionEndpoint struct {
	Edition     string
	Address     string
	Port        string
	JoinAddress string
}
type connectionMethod struct {
	ID        string
	Name      string
	Note      string
	Status    string
	Available bool
	Default   bool
	Endpoints []connectionEndpoint
}
type connectionGuide struct {
	Configured  bool
	Server      string
	ServerName  string
	Bedrock     bool
	JavaPort    int
	BedrockPort int
	Methods     []connectionMethod
}
type connectionStatusAPI interface {
	RemoteAccessStatus(context.Context, string) (api.RemoteAccessStatus, error)
}

func (a *App) howToConnect(w http.ResponseWriter, r *http.Request) {
	if mustChange(r) {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	session, ok := sessionFromRequest(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	client, ok := a.api.(sessionIdentityAPI)
	if !ok {
		http.Error(w, "role-aware controls are unavailable", http.StatusServiceUnavailable)
		return
	}
	identity, err := client.Session(r.Context(), session)
	if err != nil {
		if errors.Is(err, api.ErrPasswordChangeRequired) {
			a.handleDashboardError(w, r, err)
		} else {
			a.handleRolePageError(w, r, err)
		}
		return
	}
	if identity.MustChange {
		http.Redirect(w, r, "/password", http.StatusSeeOther)
		return
	}
	switch identity.Role {
	case "administrator", "operator", "viewer":
	default:
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	status, err := a.api.Status(r.Context(), session)
	if err != nil {
		a.handleDashboardError(w, r, err)
		return
	}
	instances, err := a.independentServerInventory(r.Context(), session)
	if err != nil {
		a.handleDashboardError(w, r, err)
		return
	}
	selected := selectedServerContext{}
	if _, explicit := r.URL.Query()["server"]; explicit || len(instances) == 1 {
		var valid bool
		selected, valid = a.selectedMinecraft(w, r, session)
		if !valid {
			return
		}
	}
	choices := []selectedServerContext{}
	for _, instance := range instances {
		name := strings.TrimSpace(instance.DisplayName)
		if name == "" {
			name = "Current server"
		}
		choices = append(choices, selectedServerContext{ID: instance.ID, Name: name})
	}
	status.Minecraft = (api.Status{}).Minecraft
	if selected.ID != "" {
		client, ok := a.api.(interface {
			MinecraftInstanceConfiguration(context.Context, string, string) (api.InstanceRuntimeSettings, error)
		})
		if ok {
			configuration, configErr := client.MinecraftInstanceConfiguration(r.Context(), session, selected.ID)
			if configErr == nil {
				status.Minecraft = instancePresentation(configuration).Minecraft
			} else if errors.Is(configErr, api.ErrUnauthorized) || errors.Is(configErr, api.ErrPasswordChangeRequired) {
				a.handleDashboardError(w, r, configErr)
				return
			}
		}
	}
	var remote api.RemoteAccessStatus
	if status.Minecraft.Configured {
		if client, ok := a.api.(connectionStatusAPI); ok {
			remote, err = client.RemoteAccessStatus(r.Context(), session)
			if err != nil {
				a.handleDashboardError(w, r, err)
				return
			}
		}
	}
	a.render(w, "how_to_connect.html", pageData{Title: "How to connect", SelectedMinecraftID: selected.ID, ConnectionServers: choices, ConnectionGuide: newConnectionGuide(status, remote, selected.Name)})
}

func usableConnectionIP(value string, ipv4 bool) bool {
	ip, err := netip.ParseAddr(value)
	return err == nil && (!ipv4 || ip.Is4()) && ip.Zone() == "" && ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}

func validConnectionPort(port int) bool { return port > 0 && port <= 65535 }

var connectionNumericHost = regexp.MustCompile(`^[0-9.]+$`)
var connectionHostname = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

func validConnectionHost(host string) bool {
	if usableConnectionIP(host, false) {
		return true
	}
	if connectionNumericHost.MatchString(host) || len(host) > 253 || strings.ContainsAny(host, ":/@\\ \t\r\n") {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !connectionHostname.MatchString(label) {
			return false
		}
	}
	return host != ""
}

// The producer forwards Playit display_address unchanged. An explicit public
// port is split for Bedrock; a bare hostname remains bare and gains no local port.
func playitConnectionEndpoint(value, edition string) (connectionEndpoint, bool) {
	host, port, err := net.SplitHostPort(value)
	if err != nil {
		if !validConnectionHost(value) {
			return connectionEndpoint{}, false
		}
		return connectionEndpoint{Edition: edition, Address: value, JoinAddress: value}, true
	}
	number, err := strconv.Atoi(port)
	if err != nil || !validConnectionPort(number) || !validConnectionHost(host) {
		return connectionEndpoint{}, false
	}
	return connectionEndpoint{Edition: edition, Address: host, Port: port, JoinAddress: value}, true
}
func localConnectionEndpoints(host string, status api.Status) []connectionEndpoint {
	endpoints := []connectionEndpoint{}
	add := func(edition string, port int) {
		if validConnectionPort(port) {
			endpoints = append(endpoints, connectionEndpoint{Edition: edition, Address: host, Port: strconv.Itoa(port), JoinAddress: net.JoinHostPort(host, strconv.Itoa(port))})
		}
	}
	add("Java", status.Minecraft.JavaPort)
	if status.Minecraft.Bedrock {
		add("Bedrock", status.Minecraft.BedrockPort)
	}
	return endpoints
}
func newConnectionGuide(status api.Status, remote api.RemoteAccessStatus, serverName string) *connectionGuide {
	mc := status.Minecraft
	software := mc.ServerSoftware
	if software == "" {
		software = mc.ServerType
	}
	serverName = strings.TrimSpace(serverName)
	if serverName == "" {
		serverName = "Current server"
	}
	guide := &connectionGuide{Configured: mc.Configured, Server: serverName, ServerName: serverName, Bedrock: mc.Bedrock, JavaPort: mc.JavaPort, BedrockPort: mc.BedrockPort}
	if !mc.Configured {
		return guide
	}
	if label := strings.TrimSpace(software + " " + mc.Version); label != "" {
		guide.Server += " — " + label
	}
	guide.Methods = []connectionMethod{
		{ID: "custom", Name: "Custom domain", Note: "Use the hostname supplied by the server owner.", Available: validConnectionPort(mc.JavaPort), Status: "Enter your hostname"},
		{ID: "local", Name: "Local connection IPv4", Note: "Works only for devices on the same home/local network.", Status: "Unavailable"},
		{ID: "tailscale", Name: "Tailscale", Note: "Works only for users with access to the same Tailscale network.", Status: "Unavailable"},
		{ID: "netbird", Name: "NetBird", Note: "Works only for peers with access to the relevant NetBird network.", Status: "Unavailable"},
		{ID: "playit", Name: "Playit.gg", Note: "Use the public tunnel connection information below. Public ports may differ from local ports.", Status: "Unavailable"},
	}
	local := &guide.Methods[1]
	if usableConnectionIP(status.System.IPv4, true) {
		local.Endpoints = localConnectionEndpoints(status.System.IPv4, status)
		local.Available = len(local.Endpoints) > 0
		local.Default = local.Available
		if local.Available {
			local.Status = "Available"
		}
	}
	for i := 2; i < len(guide.Methods); i++ {
		method := &guide.Methods[i]
		for _, provider := range remote.Providers {
			if provider.ID != method.ID {
				continue
			}
			// Friendly producer states only; never echo arbitrary setup/auth text.
			switch provider.Summary {
			case "Connected", "Not connected", "Stopped", "Connecting", "Stopping", "Not configured", "Unavailable", "Disabled", "No tunnels", "Running", "Setup pending", "Awaiting approval", "Starting":
				method.Status = provider.Summary
			}
			if !provider.Connected {
				break
			}
			if method.ID != "playit" {
				if usableConnectionIP(provider.IP, false) {
					method.Endpoints = localConnectionEndpoints(provider.IP, status)
				}
			} else if provider.ServiceActive {
				for _, tunnel := range provider.Tunnels {
					if tunnel.Disabled {
						continue
					}
					host, port, err := net.SplitHostPort(tunnel.Destination)
					if err != nil {
						continue
					}
					// Known local destination format: host:port. Do not match remote hosts
					// or ambiguous equal Java/Bedrock ports without protocol evidence.
					if host != "127.0.0.1" && host != "::1" && !(usableConnectionIP(status.System.IPv4, true) && host == status.System.IPv4) {
						continue
					}
					number, err := strconv.Atoi(port)
					if err != nil || !validConnectionPort(number) {
						continue
					}
					edition := ""
					if number == mc.JavaPort {
						edition = "Java"
					}
					if mc.Bedrock && number == mc.BedrockPort {
						if edition != "" {
							continue
						}
						edition = "Bedrock"
					}
					if edition == "" {
						continue
					}
					if endpoint, ok := playitConnectionEndpoint(tunnel.DisplayAddress, edition); ok {
						method.Endpoints = append(method.Endpoints, endpoint)
					}
				}
			}
			method.Available = len(method.Endpoints) > 0
			if method.Available {
				method.Status = "Available"
			} else if provider.Connected {
				method.Status = "Connected — no usable connection address"
			}
			break
		}
	}
	return guide
}
