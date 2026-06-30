package resilience

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Example 展示如何使用各种韧性模式
func Example() {
	// 创建一个上下文
	ctx := context.Background()

	// 示例1: 单独使用熔断器
	fmt.Println("=== 示例1: 单独使用熔断器 ===")
	circuitBreaker := NewCircuitBreaker(CircuitBreakerConfig{
		Name:             "exampleCB",
		FailureThreshold: 3,
		SuccessThreshold: 2,
		Timeout:          5 * time.Second,
	})

	// 模拟连续失败，触发熔断器打开
	for i := 0; i < 5; i++ {
		err := circuitBreaker.Execute(ctx, func() error {
			fmt.Printf("尝试调用服务，熔断器状态: %v\n", circuitBreaker.State())
			return errors.New("service unavailable")
		})
		fmt.Printf("调用结果: %v\n", err)
	}

	// 示例2: 单独使用限流器
	fmt.Println("\n=== 示例2: 单独使用限流器 ===")
	rateLimiter := NewRateLimiter(RateLimiterConfig{
		Name:  "exampleRL",
		Rate:  5.0, // 每秒5个令牌
		Burst: 2,   // 最大突发2个请求
	})

	// 连续发送请求，测试限流效果
	for i := 0; i < 10; i++ {
		allowed := rateLimiter.Allow(ctx)
		fmt.Printf("请求 %d: 是否允许通过: %v\n", i+1, allowed)
		if !allowed {
			// 等待一小段时间，让令牌桶补充一些令牌
			time.Sleep(200 * time.Millisecond)
		}
	}

	// 示例3: 单独使用隔板
	fmt.Println("\n=== 示例3: 单独使用隔板 ===")
	bulkhead := NewBulkhead(BulkheadConfig{
		Name:               "exampleBH",
		MaxConcurrentCalls: 2,
		QueueSize:          3,
		WaitTimeout:        1 * time.Second,
	})

	// 模拟多个并发请求
	for i := 0; i < 8; i++ {
		index := i
		go func() {
			err := bulkhead.Execute(ctx, func() error {
				fmt.Printf("隔板内执行任务 %d, 当前运行数: %d\n", index, bulkhead.Running())
				// 模拟处理时间
				time.Sleep(500 * time.Millisecond)
				return nil
			})
			fmt.Printf("任务 %d 完成，错误: %v\n", index, err)
		}()
	}

	// 等待所有任务完成
	time.Sleep(3 * time.Second)

	// 示例4: 单独使用重试
	fmt.Println("\n=== 示例4: 单独使用重试 ===")
	retry := NewRetry(RetryConfig{
		Name:            "exampleR",
		MaxRetries:      3,
		InitialInterval: 100 * time.Millisecond,
		Multiplier:      2.0,
	})

	// 模拟一个可能失败但最终会成功的操作
	attempt := 0
	err := retry.Execute(ctx, func() error {
		attempt++
		fmt.Printf("重试尝试 %d\n", attempt)
		if attempt < 3 {
			return errors.New("temporary failure")
		}
		return nil
	})
	fmt.Printf("最终结果: %v, 总尝试次数: %d\n", err, attempt)

	// 示例5: 组合使用多种韧性模式
	fmt.Println("\n=== 示例5: 组合使用多种韧性模式 ===")

	// 创建各种韧性组件
	cb := NewCircuitBreaker(CircuitBreakerConfig{Name: "compositeCB"})
	rl := NewRateLimiter(RateLimiterConfig{Name: "compositeRL", Rate: 10.0})
	bh := NewBulkhead(BulkheadConfig{Name: "compositeBH"})
	r := NewRetry(RetryConfig{Name: "compositeR", MaxRetries: 2})

	// 创建组合的中间件
	compositeMiddleware := NewCompositeResilience(cb, rl, bh, r)

	// 使用组合中间件执行函数
	for i := 0; i < 5; i++ {
		idx := i
		go func() {
			err := compositeMiddleware(func(ctx context.Context) error {
				fmt.Printf("组合模式下执行任务 %d\n", idx)
				// 模拟50%的失败率
				if idx%2 == 0 {
					return errors.New("random failure")
				}
				return nil
			})(ctx)
			fmt.Printf("组合模式下任务 %d 完成，错误: %v\n", idx, err)
		}()
	}

	// 等待所有任务完成
	time.Sleep(2 * time.Second)
}

// ExampleGRPCClientInterceptor 展示如何创建一个gRPC客户端拦截器，集成各种韧性模式
func ExampleGRPCClientInterceptor() {
	// 在实际应用中，你可以创建这样的拦截器并将其应用到gRPC客户端
	//
	// circuitBreaker := NewCircuitBreaker(CircuitBreakerConfig{Name: "grpcClient"})
	// rateLimiter := NewRateLimiter(RateLimiterConfig{Name: "grpcClient", Rate: 100})
	// bulkhead := NewBulkhead(BulkheadConfig{Name: "grpcClient"})
	// retry := NewRetry(RetryConfig{Name: "grpcClient"})
	//
	// interceptor := func(ctx context.Context, method string, req, reply interface{},
	//	cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	//
	//	// 创建一个执行器，包装gRPC调用
	//	executor := func(ctx context.Context) error {
	//		return invoker(ctx, method, req, reply, cc, opts...)
	//	}
	//
	//	// 使用组合的韧性模式执行调用
	//	return ExecuteWithResilience(ctx, executor,
	//		CircuitBreakerMiddleware(circuitBreaker),
	//		RateLimiterMiddleware(rateLimiter),
	//		BulkheadMiddleware(bulkhead),
	//		RetryMiddleware(retry),
	//	)
	// }
	//
	// // 创建带有拦截器的gRPC客户端连接
	// conn, err := grpc.Dial(address, grpc.WithUnaryInterceptor(interceptor))
}

// ExampleHTTPClient 展示如何在HTTP客户端中使用韧性模式
func ExampleHTTPClient() {
	// 在实际应用中，你可以这样包装HTTP客户端调用
	//
	// circuitBreaker := NewCircuitBreaker(CircuitBreakerConfig{Name: "httpClient"})
	// rateLimiter := NewRateLimiter(RateLimiterConfig{Name: "httpClient", Rate: 50})
	//
	// // 创建一个包装了韧性模式的HTTP请求函数
	// resilientDo := func(ctx context.Context, req *http.Request) (*http.Response, error) {
	//	var resp *http.Response
	//	var respErr error
	//
	//	// 使用熔断器和限流器包装请求
	//	err := ExecuteWithResilience(ctx, func(ctx context.Context) error {
	//		if !rateLimiter.Allow(ctx) {
	//			return ErrRateLimitExceeded
	//		}
	//
	//		var err error
	//		resp, err = http.DefaultClient.Do(req.WithContext(ctx))
	//		respErr = err
	//		return err
	//	}, CircuitBreakerMiddleware(circuitBreaker))
	//
	//	if err != nil {
	//		return nil, err
	//	}
	//
	//	return resp, respErr
}
