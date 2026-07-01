package sse

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Message 定义推送的消息结构
type Message struct {
	Event string // 事件名称，前端可根据此名称监听
	Data  string // 消息内容
	ID    string // 消息ID（可选，用于断线重传）
}

// Client 客户端连接实例
type Client struct {
	ID      string
	MsgChan chan Message // 每个客户端独立的消息通道
}

// SSEServer SSE 服务实例
type SSEServer struct {
	mutex      sync.RWMutex       // 读写锁，保证并发安全
	clients    map[string]*Client // 存储所有连接的客户端
	bufferSize int                // 通道缓冲大小
}

var SseServer *SSEServer

// NewSSEServer 创建一个新的 SSE 实例
func NewSSEServer(bufferSize int) *SSEServer {
	if bufferSize <= 0 {
		bufferSize = 100 // 默认缓冲
	}
	SseServer = &SSEServer{
		clients:    make(map[string]*Client),
		bufferSize: bufferSize,
	}
	return SseServer
}

// Start 启动 SSE 服务实例
// 可用于初始化资源、启动后台监控协程等
func (s *SSEServer) Start() {
	fmt.Println("[SSE] Server instance started")
}

// Stop 停止服务，清理所有连接
func (s *SSEServer) Stop() {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	fmt.Printf("[SSE] Server stopping, closing %d connections...\n", len(s.clients))

	for id, client := range s.clients {
		close(client.MsgChan) // 关闭通道，触发客户端退出
		delete(s.clients, id)
	}
	fmt.Println("[SSE] Server stopped")
}

// Connect 连接处理函数 (注册到 Gin 路由)
// 参数 clientID 从 Query 或 Param 中获取，用于标识用户
func (s *SSEServer) Connect() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. 获取客户端标识 (实际项目中可能从 Token 解析或 Query 获取)
		clientID := c.Query("client_id")
		if clientID == "" {
			// 如果没有ID，可以生成一个 UUID，或者拒绝连接
			c.JSON(http.StatusBadRequest, gin.H{"error": "client_id is required"})
			return
		}

		// 2. 设置 SSE 必要的响应头
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		// 解决 Nginx 等代理层的缓冲问题
		c.Header("X-Accel-Buffering", "no")

		// 3. 获取 Flusher
		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Streaming unsupported"})
			return
		}

		// 4. 注册客户端
		client := &Client{
			ID:      clientID,
			MsgChan: make(chan Message, s.bufferSize),
		}

		s.mutex.Lock()
		// 如果旧连接存在，先踢掉（单点登录逻辑）
		// 注意：不能直接 close oldClient.MsgChan，因为旧连接的 goroutine
		// 可能仍然持有 oldClient 引用并在 removeClient 中再次 close，
		// 导致 double-close panic。改为从 map 中移除旧客户端。
		if _, exists := s.clients[clientID]; exists {
			delete(s.clients, clientID)
		}
		s.clients[clientID] = client
		s.mutex.Unlock()

		fmt.Printf("[SSE] Client connected: %s\n", clientID)

		// 5. 发送初始连接成功消息
		s.Publish(clientID, Message{Event: "system", Data: "connected successfully"})
		// s.sendToClient(client, Message{Event: "system", Data: "connected successfully"})

		// 6. 监听消息并写入响应流
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
				fmt.Fprint(c.Writer, ": keepalive\n\n")
				flusher.Flush()

			case msg, ok := <-client.MsgChan:
				if !ok {
					// 通道被关闭（服务端主动 Shutdown 或踢下线）
					return
				}

				// 写入 SSE 格式数据
				if msg.Event != "" {
					fmt.Fprintf(c.Writer, "event: %s\n", msg.Event)
				}
				if msg.ID != "" {
					fmt.Fprintf(c.Writer, "id: %s\n", msg.ID)
				}
				fmt.Fprintf(c.Writer, "data: %s\n\n", msg.Data)

				// 立即刷新
				flusher.Flush()
			}
		}
	}
}

// Publish 向指定 ID 的客户端发送消息
func (s *SSEServer) Publish(clientID string, msg Message) bool {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	client, exists := s.clients[clientID]
	if !exists {
		fmt.Printf("[SSE] Client %s not found\n", clientID)
		return false
	}

	// 非阻塞发送，防止因为某个客户端消费过慢阻塞整个服务
	select {
	case client.MsgChan <- msg:
		return true
	default:
		fmt.Printf("[SSE] Client %s buffer full, message dropped\n", clientID)
		return false
	}
}

// Notify 广播消息给所有连接的客户端
func (s *SSEServer) Notify(msg Message) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	// 并发发送优化：如果连接数很多，可以使用协程池或 WaitGroup
	// 这里演示简单的遍历
	count := 0
	for _, client := range s.clients {
		select {
		case client.MsgChan <- msg:
			count++
		default:
			// 忽略缓冲区满的客户端
		}
	}
	fmt.Printf("[SSE] Broadcast message to %d clients\n", count)
}

// GetClientCount 获取当前连接数 (附加功能)
func (s *SSEServer) GetClientCount() int {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	return len(s.clients)
}

// --- 内部辅助方法 ---

// removeClient 安全移除客户端
func (s *SSEServer) removeClient(clientID string, current *Client) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	if client, exists := s.clients[clientID]; exists {
		if current != nil && client != current {
			return
		}
		close(client.MsgChan)
		delete(s.clients, clientID)
		fmt.Printf("[SSE] Client removed: %s\n", clientID)
	}
}

// sendToClient 内部直接发送方法
func (s *SSEServer) sendToClient(client *Client, msg Message) {
	select {
	case client.MsgChan <- msg:
	default:
	}
}
