package observability

import (
	"os"

	"github.com/natefinch/lumberjack"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger 初始化带轮转功能的 zap 日志实例
// 定义为 3 参数以匹配 main.go 的调用
func NewLogger(levelStr string, format string, logPath string) (*zap.Logger, error) {
	var level zapcore.Level
	if err := level.UnmarshalText([]byte(levelStr)); err != nil {
		level = zap.InfoLevel
	}

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder

	var encoder zapcore.Encoder
	if format == "json" {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	// 默认为 logs/agent.log
	if logPath == "" {
		logPath = "logs/agent.log"
	}

	writer := zapcore.AddSync(&lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    100, // MB
		MaxBackups: 3,
		MaxAge:     28, // days
		Compress:   true,
	})

	core := zapcore.NewTee(
		zapcore.NewCore(encoder, writer, level),
		zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), level),
	)

	return zap.New(core, zap.AddCaller()), nil
}
