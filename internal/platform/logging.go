package platform

import (
	"io"
	"log/slog"
	"strings"
)

// NewLogger builds the process logger. "json" is recommended for production;
// "text" is easier to read during development.
func NewLogger(level slog.Level, format string, out io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(format, "text") {
		return slog.New(slog.NewTextHandler(out, opts))
	}
	return slog.New(slog.NewJSONHandler(out, opts))
}

// ParseLogLevel maps a human level name to slog.Level.
func ParseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, &LogLevelError{Raw: raw}
	}
}

type LogLevelError struct{ Raw string }

func (e *LogLevelError) Error() string { return "platform: unknown log level " + e.Raw }
