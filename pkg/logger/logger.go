package logger

import (
	"io"
	"os"
	"time"

	"github.com/amkarkhi/jigsaw/pkg/types"
	"github.com/rs/zerolog"
)

// ZeroLogger wraps zerolog.Logger to implement types.Logger interface
type ZeroLogger struct {
	logger zerolog.Logger
}

// New creates a new ZeroLogger instance
func New(level string, pretty bool) types.Logger {
	var output io.Writer = os.Stdout
	
	if pretty {
		output = zerolog.ConsoleWriter{
			Out:        os.Stdout,
			TimeFormat: time.RFC3339,
		}
	}
	
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	
	logLevel := parseLevel(level)
	logger := zerolog.New(output).
		Level(logLevel).
		With().
		Timestamp().
		Caller().
		Logger()
	
	return &ZeroLogger{logger: logger}
}

// Debug logs debug message
func (z *ZeroLogger) Debug(msg string, fields map[string]any) {
	event := z.logger.Debug()
	addFields(event, fields)
	event.Msg(msg)
}

// Info logs info message
func (z *ZeroLogger) Info(msg string, fields map[string]any) {
	event := z.logger.Info()
	addFields(event, fields)
	event.Msg(msg)
}

// Warn logs warning message
func (z *ZeroLogger) Warn(msg string, fields map[string]any) {
	event := z.logger.Warn()
	addFields(event, fields)
	event.Msg(msg)
}

// Error logs error message
func (z *ZeroLogger) Error(msg string, err error, fields map[string]any) {
	event := z.logger.Error()
	if err != nil {
		event = event.Err(err)
	}
	addFields(event, fields)
	event.Msg(msg)
}

// With creates a child logger with additional fields
func (z *ZeroLogger) With(fields map[string]any) types.Logger {
	ctx := z.logger.With()
	for k, v := range fields {
		ctx = ctx.Interface(k, v)
	}
	return &ZeroLogger{logger: ctx.Logger()}
}

// addFields adds map fields to zerolog event
func addFields(event *zerolog.Event, fields map[string]any) {
	if fields == nil {
		return
	}
	for k, v := range fields {
		event.Interface(k, v)
	}
}

// parseLevel converts string level to zerolog.Level
func parseLevel(level string) zerolog.Level {
	switch level {
	case "debug":
		return zerolog.DebugLevel
	case "info":
		return zerolog.InfoLevel
	case "warn":
		return zerolog.WarnLevel
	case "error":
		return zerolog.ErrorLevel
	default:
		return zerolog.InfoLevel
	}
}
