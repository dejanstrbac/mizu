package logging

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Setup initializes a zap logger based on the provided format and verbosity.
// It also replaces the global logger and redirects the standard log output.
func Setup(format string, verbose bool) (*zap.Logger, error) {
	var config zap.Config

	if format == "json" {
		config = zap.NewProductionConfig()
	} else {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		config.EncoderConfig.EncodeTime = zapcore.TimeEncoderOfLayout("2006-01-02 15:04:05.000")
	}

	if !verbose {
		config.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	} else {
		config.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
	}

	logger, err := config.Build()
	if err != nil {
		return nil, err
	}

	// Replace global logger
	zap.ReplaceGlobals(logger)

	// Redirect standard log to zap
	zap.RedirectStdLog(logger)

	return logger, nil
}
