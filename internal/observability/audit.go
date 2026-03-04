package observability

import (
	"time"

	"github.com/natefinch/lumberjack"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var auditLogger *zap.Logger

func InitAuditLogger(path string) error {
	if path == "" {
		path = "logs/audit.log"
	}

	writer := zapcore.AddSync(&lumberjack.Logger{
		Filename:   path,
		MaxSize:    50, // MB
		MaxBackups: 5,
		MaxAge:     90, // 保留更久的审计日志
		Compress:   true,
	})

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderConfig),
		writer,
		zap.InfoLevel,
	)

	auditLogger = zap.New(core)
	return nil
}

func Audit(action string, success bool, details map[string]interface{}) {
	if auditLogger == nil { return }
	fields := []zap.Field{
		zap.String("action", action),
		zap.Bool("success", success),
		zap.Time("timestamp", time.Now()),
	}
	for k, v := range details {
		fields = append(fields, zap.Any(k, v))
	}
	auditLogger.Info("audit_event", fields...)
}
