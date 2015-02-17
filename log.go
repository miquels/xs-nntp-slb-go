//
//	Package that provides an alternative to 'log'
//	The interface is similar (but not the same)
//	Can write to stderr or syslog or both
//
//	TODO: somehow log stack trace from panic() as well..
//
package main

import (
	"fmt"
	"log"
	"log/syslog"
	"os"
	"path/filepath"
	"time"
)

const (
	LogSyslog = 1 << iota
	LogStderr
)

const (
	_ = iota
	LogDebug
	LogInfo
	LogNotice
	LogError
)

type Slog struct {
	s *syslog.Writer
	t int
}

var Log Slog
var logLabel string
var logDebug bool

func (l *Slog) initSyslog() {
	s, err := syslog.New(syslog.LOG_NEWS|syslog.LOG_NOTICE, logLabel)
	if err != nil {
		panic("syslog.New failed")
	}
	l.s = s
	return
}

func (l *Slog) syslogWriteString(level int, msg string) {
	switch level {
		case LogDebug:  _ = l.s.Debug(msg)
		case LogInfo:   _ = l.s.Info(msg)
		case LogNotice: _ = l.s.Notice(msg)
		case LogError:  _ = l.s.Err(msg)
		default:        _ = l.s.Err(msg)
	}
}

func (l *Slog) stderrWriteString(level int, msg string) {
	t := time.Now()
	fmt.Fprintf(os.Stderr, "%s %s: %s\n",
		t.Format(time.Stamp), logLabel, msg)
}

func (l *Slog) WriteString(level int, msg string) {
	if (l.t & LogSyslog) != 0 {
		l.syslogWriteString(level, msg)
	}
	if (l.t & LogStderr) != 0 {
		l.stderrWriteString(level, msg)
	}
}

func (l *Slog) Write(msg []byte) (n int, err error) {
	l.WriteString(LogError, string(msg))
	n = len(msg)
	return
}

func (l *Slog) SetOutput(fl int) {
	switch {
		case (fl & LogSyslog) != 0:
		case (fl & LogStderr) != 0:
	}
	l.t = fl
}

func (l *Slog) Printf(level int, format string, a ...interface{}) {
	var s string
	if len(a) == 0 {
		s = format
	} else {
		s = fmt.Sprintf(format, a...)
	}
	l.WriteString(level, ChompString(s))
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
	Log.t = LogSyslog

	Log.initSyslog()

	log.SetOutput(&Log)
	log.SetFlags(0)
	log.SetPrefix("")
}

