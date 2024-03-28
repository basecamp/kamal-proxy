package server

import (
	"io"
	"log/slog"
)

func CreateECSLogger(level slog.Level, out io.Writer) *slog.Logger {
	convertToECSNaming := func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey {
			return slog.Attr{Key: "@timestamp", Value: a.Value}
		}
		if a.Key == slog.LevelKey {
			return slog.Attr{Key: "log.level", Value: a.Value}
		}
		if a.Key == slog.MessageKey {
			return slog.Attr{Key: "message", Value: a.Value}
		}
		return a
	}

	handler := slog.NewJSONHandler(
		out,
		&slog.HandlerOptions{
			Level:       level,
			ReplaceAttr: convertToECSNaming,
		},
	)

	return slog.New(handler)
}
