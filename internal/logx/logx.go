package logx

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

type Logger struct {
	mu    sync.Mutex
	out   io.Writer
	level Level
}

var defaultLogger = New(os.Stderr, LevelInfo)

func New(out io.Writer, level Level) *Logger {
	return &Logger{out: out, level: level}
}

func Default() *Logger { return defaultLogger }

func SetLevel(l Level) { defaultLogger.level = l }

func (l *Logger) logf(level Level, prefix, format string, args ...any) {
	if level < l.level {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ts := time.Now().Format("15:04:05.000")
	fmt.Fprintf(l.out, "%s %s ", ts, prefix)
	fmt.Fprintf(l.out, format, args...)
	fmt.Fprintln(l.out)
}

func (l *Logger) Debugf(format string, args ...any) { l.logf(LevelDebug, "DEBUG", format, args...) }
func (l *Logger) Infof(format string, args ...any)  { l.logf(LevelInfo, "INFO ", format, args...) }
func (l *Logger) Warnf(format string, args ...any)  { l.logf(LevelWarn, "WARN ", format, args...) }
func (l *Logger) Errorf(format string, args ...any) { l.logf(LevelError, "ERROR", format, args...) }

func Debugf(format string, args ...any) { defaultLogger.Debugf(format, args...) }
func Infof(format string, args ...any)  { defaultLogger.Infof(format, args...) }
func Warnf(format string, args ...any)  { defaultLogger.Warnf(format, args...) }
func Errorf(format string, args ...any) { defaultLogger.Errorf(format, args...) }
