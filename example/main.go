// example 展示 modular 框架的最小可运行用法。
//
// 运行：go run ./example
// 访问：curl http://127.0.0.1:18080/health
// 退出：Ctrl+C 或 kill（触发优雅关闭）
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"

	"modular/packages/app"
	"modular/packages/config"
	"modular/packages/core"
	httpserver "modular/packages/transport/server/http"
)

func main() {
	// 支持取消的基础 Context：捕获 Ctrl+C / SIGTERM（容器/K8s 退出信号）
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// 1. 基础设施资源：实现 core.Resource 接口。
	// Application 会在所有 Endpoint 启动前按 FIFO 调用 Setup，
	// 在所有 Endpoint 停止后按 LIFO 调用 Close（与注册顺序相反）。
	db := newMockResource("database")
	cache := newMockResource("redis")

	// 2. 传输层 Endpoint：实现 core.Endpoint 接口。
	// transport/server/http.Server 封装了 gin，构造即监听，自带 /health 健康检查。
	httpServer, err := httpserver.NewServer(&config.HTTP{
		Host: "0.0.0.0",
		Port: 18080,
	})
	if err != nil {
		fmt.Printf("创建 HTTP server 失败: %v\n", err)
		return
	}
	// 注册业务路由（/health 由 NewServer 默认注册，无需手动添加）
	httpServer.RegisterRoute(func(e *gin.Engine) {
		e.GET("/hello", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"message": "hello from modular"})
		})
	})

	// 3. 服务节点元数据：一个 Application 对应一个 ServiceNode，
	// 由 core.NewServiceNode 从身份与传输配置构造，ID 自动生成。
	// 当注入 Registrar（如 consul.Registry）时，Application 负责在启停时注册/反注册。
	node := core.NewServiceNode(
		core.Identity{Name: "example-service", Version: "v1.0.0"},
		core.Transport{Protocol: "http", Address: "127.0.0.1", Port: 18080, HealthPath: "/health"},
	)

	// 4. 组装 Application：只做编排，不处理具体逻辑。
	// Option 顺序不影响执行：Resource 总是 FIFO 启动 / LIFO 关闭，Endpoint 总是最后启停。
	application, err := app.NewApplication(ctx, &config.Application{
		Name:    "example-service",
		Mode:    "dev",
		Version: "v1.0.0",
	},
		app.WithResource(db),        // 基础设施（Setup FIFO / Close LIFO）
		app.WithResource(cache),    // 注册两个 resource 以观察 LIFO 关闭顺序
		app.WithEndpoint(httpServer), // 传输入口（Startup 阻塞，Shutdown 解除）
		app.WithServiceNode(node),  // 服务注册元数据（Registrar 可选）
		// app.WithRegistrar(consul), // 接入真实注册中心时取消注释
	)
	if err != nil {
		fmt.Printf("创建 Application 失败: %v\n", err)
		return
	}

	// 5. Run 阻塞运行：Endpoint 启动 → 等待退出 → Endpoint 停止 → Resource 关闭。
	// 收到信号或任一 Endpoint 退出时触发优雅关闭。
	fmt.Println("==> Application 启动，访问 http://127.0.0.1:18080/hello")
	if err := application.Run(); err != nil {
		fmt.Printf("Application 运行结束: %v\n", err)
	}
}
