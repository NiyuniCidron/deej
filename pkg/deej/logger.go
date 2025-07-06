package deej

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/omriharel/deej/pkg/deej/util"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	logDirectory = "logs"
	logFilename  = "deej-latest-run.log"
)

// isDebugMode returns true if DEEJ_DEBUG=1 is set in the environment
func isDebugMode() bool {
	return os.Getenv("DEEJ_DEBUG") == "1"
}

// NewLogger provides a logger instance for the whole program
func NewLogger() (*zap.SugaredLogger, error) {
	var loggerConfig zap.Config

	if isDebugMode() {
		loggerConfig = zap.NewDevelopmentConfig()
		loggerConfig.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
		loggerConfig.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		if err := util.EnsureDirExists(logDirectory); err != nil {
			return nil, fmt.Errorf("ensure log directory exists: %w", err)
		}
		loggerConfig = zap.NewProductionConfig()
		loggerConfig.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
		loggerConfig.OutputPaths = []string{filepath.Join(logDirectory, logFilename)}
		loggerConfig.Encoding = "console"
	}

	// all build types: make it readable
	loggerConfig.EncoderConfig.EncodeCaller = nil
	loggerConfig.EncoderConfig.EncodeTime = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Format("2006-01-02 15:04:05.000"))
	}

	loggerConfig.EncoderConfig.EncodeName = func(s string, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(fmt.Sprintf("%-27s", s))
	}

	logger, err := loggerConfig.Build()
	if err != nil {
		return nil, fmt.Errorf("create zap logger: %w", err)
	}

	// no reason not to use the sugared logger - it's fast enough for anything we're gonna do
	sugar := logger.Sugar()

	return sugar, nil
}
