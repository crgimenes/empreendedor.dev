package log

import (
	"fmt"
	"io"
	stdlog "log"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/term"
)

type Level int32

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

const (
	colorReset  = "\033[0m"
	colorCyan   = "\033[36m" // timestamp
	colorGreen  = "\033[32m" // file path
	colorYellow = "\033[33m" // line number
	colorBlue   = "\033[94m" // package.function
	colorWhite  = "\033[37m" // message
)

var (
	isTerminal    bool
	defaultLogger = newConfigured(nil)
)

type Logger struct {
	out        *stdlog.Logger
	level      atomic.Int32
	useUTC     atomic.Bool
	timeLayout atomic.Value
	prefix     atomic.Value
	flags      atomic.Int32
}

func init() {
	isTerminal = term.IsTerminal(int(os.Stdout.Fd()))
}

func newConfigured(w io.Writer) *Logger {
	if w == nil {
		w = stdlog.Default().Writer()
	}
	l := &Logger{out: stdlog.New(w, "", 0)}
	// Defaults: zero-config
	l.level.Store(int32(LevelDebug))
	l.useUTC.Store(true)
	l.timeLayout.Store("2006/01/02 15:04:05")
	l.prefix.Store("")
	l.flags.Store(0)
	return l
}

func Default() *Logger { return defaultLogger }

func (l *Logger) SetOutput(w io.Writer) {
	if w != nil {
		l.out.SetOutput(w)
	}
}
func (l *Logger) Writer() io.Writer    { return l.out.Writer() }
func (l *Logger) SetPrefix(p string)   { l.prefix.Store(p) }
func (l *Logger) Prefix() string       { v, _ := l.prefix.Load().(string); return v }
func (l *Logger) SetFlags(flag int)    { l.flags.Store(int32(flag)) }
func (l *Logger) Flags() int           { return int(l.flags.Load()) }
func (l *Logger) SetLevel(level Level) { l.level.Store(int32(level)) }
func (l *Logger) SetUTC(enable bool)   { l.useUTC.Store(enable) }
func (l *Logger) SetTimeLayout(layout string) {
	if layout != "" {
		l.timeLayout.Store(layout)
	}
}

// Wrappers
func SetOutput(w io.Writer)       { defaultLogger.SetOutput(w) }
func Writer() io.Writer           { return defaultLogger.Writer() }
func SetPrefix(p string)          { defaultLogger.SetPrefix(p) }
func Prefix() string              { return defaultLogger.Prefix() }
func SetFlags(flag int)           { defaultLogger.SetFlags(flag) }
func Flags() int                  { return defaultLogger.Flags() }
func SetLevel(level Level)        { defaultLogger.SetLevel(level) }
func SetUTC(enable bool)          { defaultLogger.SetUTC(enable) }
func SetTimeLayout(layout string) { defaultLogger.SetTimeLayout(layout) }

// API drop-in
func Print(v ...any)                 { defaultLogger.outputf(LevelInfo, 3, "%s", fmt.Sprint(v...)) }
func Printf(format string, v ...any) { defaultLogger.outputf(LevelInfo, 3, format, v...) }
func Println(v ...any) {
	defaultLogger.outputf(LevelInfo, 3, "%s", strings.TrimSuffix(fmt.Sprintln(v...), "\n"))
}

func Debug(v ...any)                 { defaultLogger.outputf(LevelDebug, 3, "%s", fmt.Sprint(v...)) }
func Debugf(format string, v ...any) { defaultLogger.outputf(LevelDebug, 3, format, v...) }
func Info(v ...any)                  { defaultLogger.outputf(LevelInfo, 3, "%s", fmt.Sprint(v...)) }
func Infof(format string, v ...any)  { defaultLogger.outputf(LevelInfo, 3, format, v...) }
func Warn(v ...any)                  { defaultLogger.outputf(LevelWarn, 3, "%s", fmt.Sprint(v...)) }
func Warnf(format string, v ...any)  { defaultLogger.outputf(LevelWarn, 3, format, v...) }
func Error(v ...any)                 { defaultLogger.outputf(LevelError, 3, "%s", fmt.Sprint(v...)) }
func Errorf(format string, v ...any) { defaultLogger.outputf(LevelError, 3, format, v...) }

func Fatal(v ...any)                 { defaultLogger.outputf(LevelError, 3, "%s", fmt.Sprint(v...)); os.Exit(1) }
func Fatalf(format string, v ...any) { defaultLogger.outputf(LevelError, 3, format, v...); os.Exit(1) }
func Fatalln(v ...any) {
	defaultLogger.outputf(LevelError, 3, "%s", strings.TrimSuffix(fmt.Sprintln(v...), "\n"))
	os.Exit(1)
}

func Panic(v ...any) { s := fmt.Sprint(v...); defaultLogger.outputf(LevelError, 3, "%s", s); panic(s) }
func Panicf(format string, v ...any) {
	s := fmt.Sprintf(format, v...)
	defaultLogger.outputf(LevelError, 3, "%s", s)
	panic(s)
}

func Panicln(v ...any) {
	s := strings.TrimSuffix(fmt.Sprintln(v...), "\n")
	defaultLogger.outputf(LevelError, 3, "%s", s)
	panic(s)
}

func Output(callDepth int, s string) error {
	defaultLogger.outputf(LevelInfo, callDepth+1, "%s", s)
	return nil
}

func colorize(color, text string) string {
	if isTerminal {
		return color + text + colorReset
	}
	return text
}

func colorizedTimestamp(ts string) string {
	return colorize(colorCyan, ts)
}

func colorizedPath(path string) string {
	return colorize(colorGreen, path)
}

func colorizedLine(line string) string {
	return colorize(colorYellow, line)
}

func colorizedFunction(fn string) string {
	return colorize(colorBlue, fn)
}

func colorizedMessage(msg string) string {
	return colorize(colorWhite, msg)
}

func (l *Logger) outputf(lv Level, callerSkip int, format string, args ...any) {
	if lv < Level(l.level.Load()) {
		return
	}
	now := time.Now()
	if l.useUTC.Load() {
		now = now.UTC()
	}
	layout, _ := l.timeLayout.Load().(string)
	ts := now.Format(layout)

	file, line, fn := caller(callerSkip + 1)
	msg := fmt.Sprintf(format, args...)

	coloredTs := colorizedTimestamp(ts)
	coloredPath := colorizedPath(file)
	coloredLine := colorizedLine(itoa(line))
	coloredFn := colorizedFunction(fn)
	coloredMsg := colorizedMessage(msg)

	var b strings.Builder
	estimatedSize := len(ts) +
		len(file) + 10 +
		len(fn) +
		len(msg) +
		100 // +100 for spaces and extras
	b.Grow(estimatedSize)

	b.WriteString(coloredTs)
	b.WriteByte(' ')
	b.WriteString(coloredPath)
	b.WriteByte(' ')
	b.WriteByte('+')
	b.WriteString(coloredLine)
	b.WriteByte(' ')
	b.WriteString(coloredFn)
	if msg != "" {
		b.WriteByte(' ')
		b.WriteString(coloredMsg)
	}
	l.out.Println(b.String())
}

func caller(skip int) (file string, line int, funcName string) {
	var pcs [1]uintptr
	if runtime.Callers(skip, pcs[:]) == 0 {
		return "unknown.go", 0, "unknown"
	}
	f, _ := runtime.CallersFrames(pcs[:]).Next()
	return f.File, f.Line, f.Function
}

func itoa(i int) string { return fmt.Sprint(i) }
