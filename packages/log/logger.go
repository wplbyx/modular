package log

import (
	"errors"
	"fmt"
	"io"
	stdlog "log"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"holographic/packages/config"
)

// 全局日志器
var lm *LoggerManager

var logger *zap.Logger

type LoggerManagerOption func(*LoggerManager)

// LoggerManager 日志管理器
type LoggerManager struct {
	// logger  *zap.SugaredLogger
	config  *config.Logging
	logger  *zap.Logger
	cores   []zapcore.Core
	closers []io.Closer // 用于存储需要关闭的资源 (如 DailyRotate)

	// atomicLevel zap.AtomicLevel
}

func GetLogger() *zap.Logger {
	if lm == nil || lm.logger == nil {
		if logger != nil {
			return logger
		}
		return zap.NewNop()
	}
	return lm.logger
}

// NewLoggerManager 创建日志管理器
func NewLoggerManager(cfg *config.Logging, options ...LoggerManagerOption) (*LoggerManager, error) {
	if cfg == nil {
		return nil, errors.New("logger config is nil")
	}

	// 创建日志目录
	if err := ensureLogDir(cfg.File.Filename); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	lm = &LoggerManager{
		config: cfg,
		// atomicLevel: zap.NewAtomicLevel(),
	}

	for _, option := range options {
		option(lm)
	}

	if len(lm.cores) == 0 {
		return nil, errors.New("logger config output is empty")
	}

	// 合并所有 Core
	core := zapcore.NewTee(lm.cores...)
	lm.logger = zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	logger = lm.logger

	// log.SetDefaultLogger(lm.logger)

	return lm, nil
}

// GetLogger 获取 zap logger 实例
func (lm *LoggerManager) GetLogger() *zap.Logger {
	return lm.logger
}

// Close 清理资源
func (lm *LoggerManager) Close() {
	if lm.logger != nil {
		_ = lm.logger.Sync()
	}
	for _, closer := range lm.closers {
		_ = closer.Close()
	}
}

// ensureLogDir 确保日志目录存在
func ensureLogDir(filename string) error {
	dir := filepath.Dir(filename)
	if dir == "" {
		return nil
	}
	return os.MkdirAll(dir, 0755)
}

func parseLevel(level string) zapcore.Level {
	switch strings.ToLower(level) {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	case "panic":
		return zapcore.PanicLevel
	case "fatal":
		return zapcore.FatalLevel
	default:
		return zapcore.InfoLevel
	}
}

// ===== 全局日志函数 =====

// Debug 记录调试日志
func Debug(args ...interface{}) {
	if logger == nil {
		return
	}
	logger.Sugar().Debug(args...)
}

// Info 记录信息日志
func Info(args ...interface{}) {
	if logger == nil {
		return
	}
	logger.Sugar().Info(args...)
}

// Warn 记录警告日志
func Warn(args ...interface{}) {
	if logger == nil {
		return
	}
	logger.Sugar().Warn(args...)
}

// Error 记录错误日志
func Error(args ...interface{}) {
	if logger == nil {
		return
	}
	logger.Sugar().Error(args...)
}

// Panic 记录恐慌日志
func Panic(args ...interface{}) {
	if logger == nil {
		stdlog.Panic(args...)
	}
	logger.Sugar().Panic(args...)
}

// Fatal 记录致命日志
func Fatal(args ...interface{}) {
	if logger == nil {
		stdlog.Fatal(args...)
	}
	logger.Sugar().Fatal(args...)
}

// Debugf 记录格式化调试日志
func Debugf(template string, args ...interface{}) {
	if logger == nil {
		return
	}
	logger.Sugar().Debugf(template, args...)
}

// Infof 记录格式化信息日志
func Infof(template string, args ...interface{}) {
	if logger == nil {
		return
	}
	logger.Sugar().Infof(template, args...)
}

// Warnf 记录格式化警告日志
func Warnf(template string, args ...interface{}) {
	if logger == nil {
		return
	}
	logger.Sugar().Warnf(template, args...)
}

// Errorf 记录格式化错误日志
func Errorf(template string, args ...interface{}) {
	if logger == nil {
		return
	}
	logger.Sugar().Errorf(template, args...)
}

// Panicf 记录格式化恐慌日志
func Panicf(template string, args ...interface{}) {
	if logger == nil {
		stdlog.Panicf(template, args...)
	}
	logger.Sugar().Panicf(template, args...)
}

// Fatalf 记录格式化致命日志
func Fatalf(template string, args ...interface{}) {
	if logger == nil {
		stdlog.Fatalf(template, args...)
	}
	logger.Sugar().Fatalf(template, args...)
}
