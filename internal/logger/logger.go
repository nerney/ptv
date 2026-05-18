package logger

import (
	"fmt"
	"log"
	"os"
)

type Level string

const (
	INFO  Level = "INFO"
	ERROR Level = "ERROR"
)

// Logger is a thin wrapper around the standard log package that produces
// structured-looking text lines on stderr. Docker captures stderr to
// `docker logs`.
type Logger struct{}

func New() *Logger {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	return &Logger{}
}

func (l *Logger) Info(category, msg string) {
	log.Printf("INFO  [%s] %s", category, msg)
}

func (l *Logger) Err(category, msg string) {
	log.Printf("ERROR [%s] %s", category, msg)
}

func (l *Logger) HTTP(level Level, category, method, url string, status int, bodyLen int) {
	log.Printf("%-5s [%s] %s %s → %d (%d bytes)", level, category, method, url, status, bodyLen)
}

// Banner writes raw multi-line text to stderr without the timestamp/level
// prefix. Intended for the startup ASCII-art banner — keeping it in the
// logger package means all process output flows through one writer.
func (l *Logger) Banner(text string) {
	fmt.Fprintln(os.Stderr, text)
}
