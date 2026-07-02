package core

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"
)

// Transport describes a monitoring endpoint of a service instance.
type Transport struct {
	Protocol   string `json:"protocol"`
	Address    string `json:"address"`
	Port       int    `json:"port"`
	HealthPath string `json:"health_path,omitempty"`
}

// Endpoint returns the full URL of this transport endpoint.
func (t Transport) Endpoint() string {
	return fmt.Sprintf("%s://%s", t.Protocol, net.JoinHostPort(t.Address, strconv.Itoa(t.Port)))
}

// ServiceNode describes the complete metadata of a service instance,
// used for service registration and discovery.
type ServiceNode struct {
	Name       string            `json:"name"`
	Version    string            `json:"version"`
	ID         string            `json:"id"`
	Transports []Transport       `json:"transports"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// NewServiceNode builds a ServiceNode from identity and transport config.
func NewServiceNode(name, version string, transports ...Transport) *ServiceNode {
	node := &ServiceNode{
		Name:       name,
		Version:    version,
		Transports: transports,
	}
	node.ID = node.generateID()
	return node
}

// Endpoints returns the URL list of all transports.
func (n *ServiceNode) Endpoints() []string {
	eps := make([]string, 0, len(n.Transports))
	for _, t := range n.Transports {
		eps = append(eps, t.Endpoint())
	}
	return eps
}

// generateID produces a deterministic unique ID from name and transports.
func (n *ServiceNode) generateID() string {
	parts := []string{n.Name}
	for _, t := range n.Transports {
		parts = append(parts, t.Protocol, t.Address, strconv.Itoa(t.Port))
	}
	return GenerateID(parts...)
}

// GenerateID produces a valid service ID from string fragments.
func GenerateID(parts ...string) string {
	joined := strings.Join(parts, "-")
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(joined) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

// NormalizeHost normalizes wildcard/empty addresses to 127.0.0.1,
// and strips IPv6 brackets.
func NormalizeHost(host string) string {
	host = strings.Trim(host, "[]")
	switch host {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	}
	return host
}