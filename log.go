package main

import (
	"fmt"
	"log/syslog"
	"os"
	"path/filepath"
	"time"
)

const (
	LogSyslog = iota
	LogStderr
)

const (
	_ = iota
	LogDebug
	LogInfo
	LogNotice
	LogError
)

type logWriter interface {
	WriteString(level int, msg string)
}

type logSyslog struct {
	w *syslog.Writer
}

type logStderr struct {
}

type Slog struct {
	w	logWriter
}

var Log Slog
var logLabel string
var logDebug bool

func newLogSyslog() (l *logSyslog) {
	l = &logSyslog{}
	w, err := syslog.New(syslog.LOG_NEWS|syslog.LOG_NOTICE, logLabel)
	if err != nil {
		panic("syslog.New failed")
	}
	l.w = w
	return
}

func (l *logSyslog) WriteString(level int, msg string) {
	switch level {
		case LogDebug:  _ = l.w.Debug(msg)
		case LogInfo:   _ = l.w.Info(msg)
		case LogNotice: _ = l.w.Notice(msg)
		case LogError:  _ = l.w.Err(msg)
		default:        _ = l.w.Err(msg)
	}
}

func (l *logStderr) WriteString(level int, msg string) {
	t := time.Now()
	fmt.Fprintf(os.Stderr, "%s %s: %s\n",
		t.Format(time.Stamp), logLabel, msg)
}

func (l *Slog) SetOutput(fl int) {
	switch fl {
		case LogSyslog: l.w = newLogSyslog()
		case LogStderr: l.w = &logStderr{}
	}
}

func (l *Slog) Printf(level int, format string, a ...interface{}) {
	var s string
	if len(a) == 0 {
		s = format
	} else {
		s = fmt.Sprintf(format, a...)
	}
	var n int
	for n = len(s); n > 0; n-- {
		if s[n-1] != '\r' && s[n-1] != '\n' {
			break
		}
	}
	l.w.WriteString(level, s[:n])
}

func (l *Slog) Debug(fmt string, a ...interface{}) {
	if (logDebug) {
		l.Printf(LogDebug, fmt, a...)
	}
}

func (l *Slog) Info(fmt string, a ...interface{}) {
	l.Printf(LogInfo, fmt, a...)
}

func (l *Slog) Notice(fmt string, a ...interface{}) {
	l.Printf(LogNotice, fmt, a...)
}

func (l *Slog) Error(fmt string, a ...interface{}) {
	l.Printf(LogError, fmt, a...)
}

func (l *Slog) Fatal(fmt string, a ...interface{}) {
	l.Printf(LogError, fmt, a...)
	os.Exit(1)
}

func init() {
	logLabel = filepath.Base(os.Args[0])
	Log.w = newLogSyslog()
}

