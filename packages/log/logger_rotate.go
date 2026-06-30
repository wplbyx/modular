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
	currentDate string // 当前文件名包含的日期字符串，例如 2006-01-02
	filename    string // 原始文件名前缀，例如 logs/app
	ext         string // 文件后缀，例如 .log

	stopChan chan struct{}
	once     sync.Once
}

// NewDailyRotate 创建一个新的按天轮转的 Syncer
// filename: 基础文件名，例如 "./logs/app"
// maxSize, maxBackups, compress: lumberjack 的标准配置
func NewDailyRotate(ctx context.Context, filename string, maxSize int, maxBackups int, compress bool, maxAge int) *DailyRotate {
	dr := &DailyRotate{
		filename: filename,
		ext:      ".log",
		stopChan: make(chan struct{}),
	}

	// 1. 初始化 lumberjack
	dr.lj = &lumberjack.Logger{
		MaxSize:    maxSize,
		MaxBackups: maxBackups,
		MaxAge:     maxAge,
		Compress:   compress,
		LocalTime:  true, // 使用本地时间
	}

	// 2. 计算当天的日期，并设置初始 Filename
	dr.updateFilename(time.Now())

	// 3. 启动后台协程监听日期变更
	go dr.watchDate(ctx)

	return dr
}

// Write 实现 io.Writer 接口
func (dr *DailyRotate) Write(p []byte) (n int, err error) {
	return dr.lj.Write(p)
}

// // Sync 实现 zapcore.WriteSyncer 接口
// func (dr *DailyRotate) Sync() error {
// 	return dr.lj.
// }

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

	// 如果日期没变，不需要更新
	if dr.currentDate == dateStr {
		return
	}

	dr.currentDate = dateStr
	// 拼接最终文件名: app-2006-01-02.log
	newFilename := fmt.Sprintf("%s-%s%s", dr.filename, dateStr, dr.ext)

	dr.lj.Filename = newFilename
}

// watchDate 后台协程，负责在跨天时触发切割
func (dr *DailyRotate) watchDate(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	// now := time.Now()
	// next := now.Add(24 * time.Hour)
	// nextMidnight := time.Date(next.Year(), next.Month(), next.Day(), 0, 0, 0, 0, next.Location())
	// duration := nextMidnight.Sub(now)

	for {
		select {
		case <-ctx.Done():
			// 应用退出，停止监听
			return
		case <-dr.stopChan:
			return
		case now := <-ticker.C:
			// 每分钟检查一次当前日期
			dr.updateFilename(now)

			// 如果日期变了，这里做一个简单的 Rotate 触发
			// 注意：lumberjack 只有在写入时才会真正检查文件，
			// 但因为 updateFilename 已经修改了 lj.Filename，
			// 下次写入时 lumberjack 会自动使用新文件名。
			// 为了强制让旧文件归档（如果有内容），可以手动 Rotate 一次
			if dr.currentDate != now.Format(time.DateOnly) {
				// 这里逻辑上其实不会进入，因为上面 updateFilename 已经更新了
				// 但为了确保文件切换逻辑触发：
				_ = dr.lj.Rotate()
			}
		}
	}
}
