package log

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelzap"
	"go.opentelemetry.io/otel/sdk/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

)

// WithOutputConsole 控制台日志输出
func WithOutputConsole() LoggerManagerOption {
	return func(manager *LoggerManager) {
		projectRoot, _ := os.Getwd()
		encoderConfig := zap.NewProductionEncoderConfig()
		encoderConfig.TimeKey = "timestamp"
		encoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout(time.DateTime)
		encoderConfig.EncodeCaller = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
			if !caller.Defined {
				return
			}

			// 获取完整绝对路径, 尝试转换为相对路径
			fullPath := caller.FullPath()
			if relativePath, err := filepath.Rel(projectRoot, fullPath); err == nil {
				enc.AppendString(relativePath)
				return
			}
			enc.AppendString(fullPath)
		}

		encoder := zapcore.NewConsoleEncoder(encoderConfig)
		syncer := zapcore.AddSync(os.Stdout)
		core := zapcore.NewCore(encoder, syncer, parseLevel(manager.config.Level))
		manager.cores = append(manager.cores, core)
	}
}

// WithOutputFiles 输出到文件
func WithOutputFiles(ctx context.Context) LoggerManagerOption {
	return func(manager *LoggerManager) {
		projectRoot, _ := os.Getwd()
		encoderConfig := zap.NewProductionEncoderConfig()
		encoderConfig.TimeKey = "timestamp"
		encoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout(time.DateTime)
		encoderConfig.EncodeCaller = func(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
			if !caller.Defined {
				return
			}

			// 获取完整绝对路径, 尝试转换为相对路径
			fullPath := caller.FullPath()
			if relativePath, err := filepath.Rel(projectRoot, fullPath); err == nil {
				enc.AppendString(relativePath)
				return
			}
			enc.AppendString(fullPath)
		}
		encoder := zapcore.NewConsoleEncoder(encoderConfig)

		// 解析文件名基础路径
		// 假设 c.File.Filename 是 "./logs/app.log"
		baseName := manager.config.File.Filename
		if strings.HasSuffix(baseName, ".log") {
			baseName = strings.TrimSuffix(baseName, ".log")
		}

		// 使用封装的 lumberjack 实现日志分片 DailyRotateSyncer
		writer := NewDailyRotate(
			ctx,
			baseName,                       // 基础路径，如 "./logs/app"
			manager.config.File.MaxSize,    // 单文件最大大小 MB
			manager.config.File.MaxBackups, // 保留旧文件的最大个数
			manager.config.File.Compress,   // 是否压缩
			manager.config.File.MaxAge,     // 保留旧文件的最大天数
		)

		syncer := zapcore.AddSync(writer)

		core := zapcore.NewCore(encoder, syncer, parseLevel(manager.config.Level))
		manager.cores = append(manager.cores, core)
	}
}

// WithOutputTelemetry 输出到远程仓库
func WithOutputTelemetry(name string, lp *log.LoggerProvider) LoggerManagerOption {
	return func(manager *LoggerManager) {
		if lp == nil {
			return
		}
		core := otelzap.NewCore(name, otelzap.WithLoggerProvider(lp))
		manager.cores = append(manager.cores, core)
	}
}
