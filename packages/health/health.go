// Package health provides composable, transport-independent readiness checks.
package health

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	StatusOK     = "ok"
	StatusFailed = "failed"
)

// State 是进程对外接流量的生命周期状态。
type State string

const (
	StateStarting State = "starting"
	StateReady    State = "ready"
	StateDraining State = "draining"
)

const (
	defaultCheckTimeout = 2 * time.Second
	defaultConcurrency  = 8
)

// Checker reports the readiness of one named dependency.
type Checker interface {
	Name() string
	Check(context.Context) error
}

// Result is the stable, serializable outcome of one Checker.
type Result struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Report is the aggregate readiness result.
type Report struct {
	Status string   `json:"status"`
	State  State    `json:"state,omitempty"`
	Checks []Result `json:"checks"`
}

// ManagerOption 配置进程级健康检查管理器。
type ManagerOption func(*Manager)

// WithCheckTimeout 设置每个 Checker 的最长执行时间。
func WithCheckTimeout(timeout time.Duration) ManagerOption {
	return func(manager *Manager) {
		if timeout > 0 {
			manager.checkTimeout = timeout
		}
	}
}

// WithMaxConcurrency 设置单次报告最多并行执行的 Checker 数量。
func WithMaxConcurrency(limit int) ManagerOption {
	return func(manager *Manager) {
		if limit > 0 {
			manager.maxConcurrency = limit
		}
	}
}

// Manager 统一管理进程 readiness 状态和依赖检查。
type Manager struct {
	mu             sync.RWMutex
	state          State
	checkers       map[string]Checker
	checkTimeout   time.Duration
	maxConcurrency int
}

// NewManager 创建 starting 状态的进程级健康检查管理器。
func NewManager(options ...ManagerOption) *Manager {
	manager := &Manager{
		state:          StateStarting,
		checkers:       make(map[string]Checker),
		checkTimeout:   defaultCheckTimeout,
		maxConcurrency: defaultConcurrency,
	}
	for _, option := range options {
		if option != nil {
			option(manager)
		}
	}
	return manager
}

// Register 注册结构化检查；nil、空名称和重复名称会返回错误。
func (manager *Manager) Register(checkers ...Checker) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	for _, checker := range checkers {
		if checker == nil {
			return errors.New("health checker is nil")
		}
		name := strings.TrimSpace(checker.Name())
		if name == "" {
			return errors.New("health checker name is empty")
		}
		if _, exists := manager.checkers[name]; exists {
			return fmt.Errorf("health checker %q is already registered", name)
		}
		manager.checkers[name] = checker
	}
	return nil
}

// SetReady 表示进程可以接收流量。
func (manager *Manager) SetReady() {
	manager.mu.Lock()
	manager.state = StateReady
	manager.mu.Unlock()
}

// SetDraining 表示进程正在摘流量和排空请求。
func (manager *Manager) SetDraining() {
	manager.mu.Lock()
	manager.state = StateDraining
	manager.mu.Unlock()
}

// State 返回当前进程 readiness 状态。
func (manager *Manager) State() State {
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.state
}

// Report 返回适合暴露给客户端的报告，不包含底层错误文本。
func (manager *Manager) Report(ctx context.Context) Report {
	return manager.report(ctx, false)
}

// DetailedReport 返回包含底层错误的内部诊断报告。
func (manager *Manager) DetailedReport(ctx context.Context) Report {
	return manager.report(ctx, true)
}

func (manager *Manager) report(ctx context.Context, details bool) Report {
	manager.mu.RLock()
	state := manager.state
	checkers := make([]Checker, 0, len(manager.checkers))
	for _, checker := range manager.checkers {
		checkers = append(checkers, checker)
	}
	timeout := manager.checkTimeout
	concurrency := manager.maxConcurrency
	manager.mu.RUnlock()

	report := run(ctx, timeout, concurrency, details, checkers...)
	report.State = state
	if state != StateReady {
		report.Status = StatusFailed
	}
	return report
}

// Handler 返回进程级 readiness HTTP handler。
func (manager *Manager) Handler() http.Handler {
	return reportHandler(manager.Report)
}

// Run executes all checks concurrently and returns results ordered by name.
func Run(ctx context.Context, checkers ...Checker) Report {
	return run(ctx, 0, defaultConcurrency, true, checkers...)
}

func run(ctx context.Context, timeout time.Duration, concurrency int, details bool, checkers ...Checker) Report {
	report := Report{Status: StatusOK, Checks: make([]Result, len(checkers))}
	var wait sync.WaitGroup
	semaphore := make(chan struct{}, concurrency)
	for i, checker := range checkers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				report.Checks[i] = failedResult("unknown", ctx.Err(), details)
				return
			}
			result := Result{Status: StatusOK}
			if checker == nil {
				report.Checks[i] = failedResult("unknown", errors.New("health checker is nil"), details)
				return
			}
			result.Name = checker.Name()
			if err := runCheck(ctx, timeout, checker); err != nil {
				result = failedResult(result.Name, err, details)
			}
			report.Checks[i] = result
		}()
	}
	wait.Wait()

	sort.SliceStable(report.Checks, func(i, j int) bool {
		return report.Checks[i].Name < report.Checks[j].Name
	})
	for _, result := range report.Checks {
		if result.Status == StatusFailed {
			report.Status = StatusFailed
			break
		}
	}
	return report
}

func runCheck(ctx context.Context, timeout time.Duration, checker Checker) error {
	checkCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		checkCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- checker.Check(checkCtx) }()
	select {
	case err := <-done:
		return err
	case <-checkCtx.Done():
		return checkCtx.Err()
	}
}

func failedResult(name string, err error, details bool) Result {
	result := Result{Name: name, Status: StatusFailed}
	if details && err != nil {
		result.Error = err.Error()
	}
	return result
}

// Handler returns a net/http readiness handler for the supplied checks.
func Handler(checkers ...Checker) http.Handler {
	return reportHandler(func(ctx context.Context) Report { return Run(ctx, checkers...) })
}

func reportHandler(report func(context.Context) Report) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		result := report(request.Context())
		status := http.StatusOK
		if result.Status == StatusFailed {
			status = http.StatusServiceUnavailable
		}
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.WriteHeader(status)
		_ = json.NewEncoder(writer).Encode(result)
	})
}
