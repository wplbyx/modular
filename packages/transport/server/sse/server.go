package sse

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/wplbyx/modular/packages/core"
)

var _ core.ReadyEndpoint = (*Server)(nil)

// Message 定义推送的消息结构。
type Message struct {
	Event string // 事件名称，前端可据此监听
	Data  string // 消息内容
	ID    string // 消息 ID（可选，用于断线重传）
}

// Client 客户端连接实例。
type Client struct {
	ID      string
	MsgChan chan Message // 每个客户端独立的消息通道
}

// Server 是一个 SSE (Server-Sent Events) 服务，实现 core.Endpoint 接口。
// SSE 是单向推送（服务器 -> 浏览器），适合实时通知、消息推送等场景。
type Server struct {
	mu         sync.RWMutex
	clients    map[string]*Client
	bufferSize int
	started    bool
	closed     bool
	cancel     context.CancelFunc
	startupID  *struct{}
	ready      chan struct{}
	readyOnce  sync.Once
}

// NewServer 创建一个新的 SSE 服务实例。
// bufferSize 为每个客户端消息通道的缓冲大小。
func NewServer(bufferSize int) *Server {
	if bufferSize <= 0 {
		bufferSize = 100
	}
	return &Server{
		clients:    make(map[string]*Client),
		bufferSize: bufferSize,
		ready:      make(chan struct{}),
	}
}

// Name 返回组件名，用于日志区分。
func (s *Server) Name() string {
	return "SSE Server"
}

// Startup 是 core.Endpoint 的启动方法。
// SSE 服务本身不需要监听端口（它挂载在 HTTP 服务的路由上），
// 因此 Startup 仅标记启动状态，阻塞等待 context 取消。
func (s *Server) Startup(ctx context.Context) error {
	startupCtx, cancel := context.WithCancel(ctx)
	startupID := &struct{}{}

	s.mu.Lock()
	if s.cancel != nil || s.closed {
		s.mu.Unlock()
		cancel()
		return fmt.Errorf("sse server is already running")
	}
	s.started = true
	s.cancel = cancel
	s.startupID = startupID
	s.mu.Unlock()
	s.readyOnce.Do(func() { close(s.ready) })

	<-startupCtx.Done()

	s.mu.Lock()
	if s.startupID == startupID {
		s.cancel = nil
		s.startupID = nil
	}
	s.started = false
	s.mu.Unlock()

	return startupCtx.Err()
}

// Ready 等待 SSE Endpoint 完成启动。
func (s *Server) Ready(ctx context.Context) error {
	select {
	case <-s.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Shutdown 关闭服务，清理所有连接。
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.startupID = nil
	s.started = false
	for id, client := range s.clients {
		close(client.MsgChan)
		delete(s.clients, id)
	}
	s.mu.Unlock()
	return nil
}

// Connect 返回 Gin 处理函数，用于注册到 HTTP 路由。
// 客户端通过 Query 参数 client_id 标识身份。
func (s *Server) Connect() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取客户端标识
		clientID := c.Query("client_id")
		if clientID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "client_id is required"})
			return
		}

		// 设置 SSE 必要的响应头
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")

		// 获取 Flusher
		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Streaming unsupported"})
			return
		}

		// 注册客户端
		client := &Client{
			ID:      clientID,
			MsgChan: make(chan Message, s.bufferSize),
		}

		if !s.addClient(clientID, client) {
			c.AbortWithStatus(http.StatusServiceUnavailable)
			return
		}
		defer s.removeClient(clientID, client)

		// 发送初始连接成功消息
		s.Publish(clientID, Message{Event: "system", Data: "connected successfully"})

		// 监听消息并写入响应流
		notify := c.Request.Context().Done()
		keepalive := time.NewTicker(25 * time.Second)
		defer keepalive.Stop()

		for {
			select {
			case <-notify:
				// 客户端断开连接（关闭浏览器、网络中断）
				s.removeClient(clientID, client)
				return

			case <-keepalive.C:
				if _, err := fmt.Fprint(c.Writer, ": keepalive\n\n"); err != nil {
					return
				}
				flusher.Flush()

			case msg, ok := <-client.MsgChan:
				if !ok {
					// 通道被关闭（服务端主动 Shutdown 或踢下线）
					return
				}

				if err := writeMessage(c.Writer, msg); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}
}

// Publish 向指定 ID 的客户端发送消息。
func (s *Server) Publish(clientID string, msg Message) bool {
	if !validMessage(msg) {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	client, exists := s.clients[clientID]
	if !exists {
		return false
	}

	// 非阻塞发送，防止因为某个客户端消费过慢阻塞整个服务
	select {
	case client.MsgChan <- msg:
		return true
	default:
		return false
	}
}

// Notify 广播消息给所有连接的客户端。
func (s *Server) Notify(msg Message) {
	if !validMessage(msg) {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, client := range s.clients {
		select {
		case client.MsgChan <- msg:
		default:
			// 忽略缓冲区满的客户端
		}
	}
}

// GetClientCount 获取当前连接数。
func (s *Server) GetClientCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.clients)
}

// IsStarted reports whether Startup is currently blocking.
func (s *Server) IsStarted() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.started
}

// --- 内部辅助方法 ---

// addClient replaces any existing connection for clientID and closes the old channel.
func (s *Server) addClient(clientID string, client *Client) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return false
	}
	if old, exists := s.clients[clientID]; exists && old != client {
		close(old.MsgChan)
	}
	s.clients[clientID] = client
	return true
}

// removeClient 安全移除客户端。
func (s *Server) removeClient(clientID string, current *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if client, exists := s.clients[clientID]; exists {
		if current != nil && client != current {
			return
		}
		close(client.MsgChan)
		delete(s.clients, clientID)
	}
}

func validMessage(msg Message) bool {
	return !strings.ContainsAny(msg.Event, "\r\n") && !strings.ContainsAny(msg.ID, "\r\n\x00")
}
func writeMessage(writer io.Writer, msg Message) error {
	if !validMessage(msg) {
		return fmt.Errorf("invalid SSE event or ID")
	}
	var output strings.Builder
	if msg.Event != "" {
		fmt.Fprintf(&output, "event: %s\n", msg.Event)
	}
	if msg.ID != "" {
		fmt.Fprintf(&output, "id: %s\n", msg.ID)
	}
	data := strings.ReplaceAll(strings.ReplaceAll(msg.Data, "\r\n", "\n"), "\r", "\n")
	for _, line := range strings.Split(data, "\n") {
		fmt.Fprintf(&output, "data: %s\n", line)
	}
	output.WriteByte('\n')
	_, err := io.WriteString(writer, output.String())
	return err
}
