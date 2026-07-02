package core

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"
)

// Transport 描述服务实例的一个监听端点。
type Transport struct {
	Protocol   string `json:"protocol"`              // "http", "https", "grpc"
	Address    string `json:"address"`               // 监听地址（已归一化）
	Port       int    `json:"port"`                  // 监听端口
	HealthPath string `json:"health_path,omitempty"` // 健康检查路径（可选）
}

// Endpoint 返回该传输端点的完整 URL。
func (t Transport) Endpoint() string {
	return fmt.Sprintf("%s://%s", t.Protocol, net.JoinHostPort(t.Address, strconv.Itoa(t.Port)))
}

// Identity 携带应用级身份信息，由 Application 从配置构造。
type Identity struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// ServiceNode 描述一个服务实例的完整元数据，用于服务注册与发现。
// 一个 Application 对应一个 ServiceNode，从启动配置构建，不依赖运行时 endpoint。
type ServiceNode struct {
	Identity
	ID         string            `json:"id"`
	Transports []Transport       `json:"transports"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// NewServiceNode 从身份和传输配置构建 ServiceNode，并自动生成唯一 ID。
func NewServiceNode(id Identity, transports ...Transport) *ServiceNode {
	node := &ServiceNode{
		Identity:   id,
		Transports: transports,
	}
	node.ID = node.generateID()
	return node
}

// Endpoints 返回所有传输端点的 URL 列表。
func (n *ServiceNode) Endpoints() []string {
	eps := make([]string, 0, len(n.Transports))
	for _, t := range n.Transports {
		eps = append(eps, t.Endpoint())
	}
	return eps
}

// generateID 根据服务名和所有传输端点生成确定性唯一 ID。
func (n *ServiceNode) generateID() string {
	parts := []string{n.Name}
	for _, t := range n.Transports {
		parts = append(parts, t.Protocol, t.Address, strconv.Itoa(t.Port))
	}
	return GenerateID(parts...)
}

// GenerateID 从多个字符串片段生成合法的服务 ID。
// 非字母数字字符统一替换为 "-"，连缀分隔符合并，首尾去除多余的 "-"。
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

// NormalizeHost 将通配/空地址归一化为 127.0.0.1，并去除 IPv6 方括号。
func NormalizeHost(host string) string {
	host = strings.Trim(host, "[]")
	switch host {
	case "", "0.0.0.0", "::":
		return "127.0.0.1"
	}
	return host
}