package log

import (
	"context"
	"fmt"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

// DailyRotate 封装了 lumberjack，增加了按天自动切换文件名的功能
type DailyRotate struct {
	lj          *lumberjack.Logger
	mu          sync.Mutex // 保护 lj.Filename 和 currentDate 的并发访问
	currentDate string     // 当前文件名包含的日期字符串，例如 2006-01-02
	filename    string     // 原始文件名前缀，例如 logs/app
	ext         string     // 文件后缀，例如 .log

	stopChan chan struct{}
	once     sync.Once
}

// NewDailyRotate 创建一个按天轮转的 Syncer
func NewDailyRotate(ctx context.Context, filename string, maxSize int, maxBackups int, compress bool, maxAge int) *DailyRotate {
	dr := &DailyRotate{
		filename: filename,
		ext:      ".log",
		stopChan: make(chan struct{}),
	}

	dr.lj = &lumberjack.Logger{
		MaxSize:    maxSize,
		MaxBackups: maxBackups,
		MaxAge:     maxAge,
		Compress:   compress,
		LocalTime:  true,
	}

	// 计算当天的日期，并设置初始 Filename
	dr.updateFilename(time.Now())

	// 启动后台协程监视日期变更
	go dr.watchDate(ctx)

	return dr
}

// Write 实现 io.Writer 接口
func (dr *DailyRotate) Write(p []byte) (n int, err error) {
	dr.mu.Lock()
	defer dr.mu.Unlock()
	return dr.lj.Write(p)
}

// Close 关闭资源
func (dr *DailyRotate) Close() error {
	dr.once.Do(func() {
		close(dr.stopChan)
	})
	return dr.lj.Close()
}

// updateFilename 根据 date 更新 lumberjack 的 Filename 字段
func (dr *DailyRotate) updateFilename(t time.Time) {
	dateStr := t.Format(time.DateOnly)

	dr.mu.Lock()
	defer dr.mu.Unlock()

	// 如果日期没变，不需要更新
	if dr.currentDate == dateStr {
		return
	}

	dr.currentDate = dateStr
	// 拼接最终文件名: app-2006-01-02.log
	dr.lj.Filename = fmt.Sprintf("%s-%s%s", dr.filename, dateStr, dr.ext)
}

// watchDate 后台协程，负责在跨天时触发切换
func (dr *DailyRotate) watchDate(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-dr.stopChan:
			return
		case now := <-ticker.C:
			dr.updateFilename(now)
		}
	}
}
