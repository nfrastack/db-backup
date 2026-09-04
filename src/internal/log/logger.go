// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package log

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	LevelTrace Level = iota
	LevelDebug
	LevelInfo
	LevelWarn
	LevelError
	LevelNone Level = -1
)

const (
	colTrace = "\x1b[46m"
	colDebug = "\x1b[45m"
	colInfo  = "\x1b[42m"
	colWarn  = "\x1b[44m"
	colError = "\x1b[101m"
	colOff   = "\x1b[49m"
)

type Format int

const (
	FormatText Format = iota
	FormatJSON
)

type LogType int

const (
	LogConsole LogType = iota
	LogFile
	LogBoth
)

const (
	LevelStyleAuto    = "auto"
	LevelStyleBracket = "bracket" // [LEVEL]
	LevelStyleKV      = "kv"      // level=<lower>
)

type Logger struct {
	mu          sync.RWMutex
	level       Level
	format      Format
	logType     LogType
	out         io.Writer
	job         string
	session     string
	logPath     string
	fileMode    string
	dirMode     string
	logUser     string
	logGroup    string
	colour      bool
	prefix      bool
	prefixFmt   string
	showSession bool
	showRunID   bool
	timings     bool
	size        string
	loc         *time.Location
	journald    bool
	levelStyle  string
	timeQuoted  bool
}

var Default = New(LevelInfo, FormatText)

// auto

type loggerSnapshot struct {
	level       Level
	format      Format
	logType     LogType
	out         io.Writer
	job         string
	session     string
	logPath     string
	fileMode    string
	dirMode     string
	logUser     string
	logGroup    string
	colour      bool
	prefix      bool
	prefixFmt   string
	showSession bool
	showRunID   bool
	loc         *time.Location
	journald    bool
	levelStyle  string
	timeQuoted  bool
}

func CurrentLevel() Level {
	Default.mu.RLock()
	defer Default.mu.RUnlock()
	return Default.level
}

func Debug(action, msg string, fields ...any)             { Default.Debug(action, msg, fields...) }
func (l *Logger) Debug(action, msg string, fields ...any) { l.Log(LevelDebug, action, msg, fields...) }

func Error(action, msg string, fields ...any)             { Default.Error(action, msg, fields...) }
func (l *Logger) Error(action, msg string, fields ...any) { l.Log(LevelError, action, msg, fields...) }

func Info(action, msg string, fields ...any)             { Default.Info(action, msg, fields...) }
func (l *Logger) Info(action, msg string, fields ...any) { l.Log(LevelInfo, action, msg, fields...) }

func Init(levelStr string, logType string, format Format) {
	Default.mu.Lock()
	defer Default.mu.Unlock()
	Default.level = ParseLevel(levelStr)
	Default.format = format
	switch strings.ToLower(logType) {
	case "file":
		Default.logType = LogFile
	case "both":
		Default.logType = LogBoth
	default:
		Default.logType = LogConsole
	}
}

func (l *Logger) Level() Level {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.level
}

func Location() *time.Location {
	Default.mu.RLock()
	defer Default.mu.RUnlock()
	return Default.loc
}

func (l *Logger) Log(level Level, action, msg string, fields ...any) {
	l.logAt(level, 0, nil, action, msg, fields...)
}

func New(level Level, format Format) *Logger {
	return &Logger{
		level:       level,
		format:      format,
		logType:     LogConsole,
		out:         os.Stderr,
		colour:      true,
		prefix:      true,
		prefixFmt:   "2006-01-02 15:04:05",
		showSession: true,
		showRunID:   true,
		timings:     true,
	}
}

func ParseLevel(s string) Level {
	switch strings.ToLower(s) {
	case "trace":
		return LevelTrace
	case "debug":
		return LevelDebug
	case "info":
		return LevelInfo
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	case "none":
		return LevelNone
	}
	return LevelInfo
}

func ResolveLocation(zone string, utc bool) *time.Location {
	if utc {
		return time.UTC
	}
	if zone != "" {
		if loc, err := time.LoadLocation(zone); err == nil {
			return loc
		}
	}
	for _, e := range []string{"TZ", "TIMEZONE"} {
		if v := os.Getenv(e); v != "" {
			if loc, err := time.LoadLocation(v); err == nil {
				return loc
			}
		}
	}
	return nil
}

func Session() string { return Default.Session() }
func (l *Logger) Session() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.session
}

func (l *Logger) SetColour(enabled bool) {
	l.mu.Lock()
	l.colour = enabled
	l.mu.Unlock()
}

func SetColour(enabled bool) { Default.SetColour(enabled) }
func SetJob(name string)     { Default.SetJob(name) }
func (l *Logger) SetJob(name string) {
	l.mu.Lock()
	l.job = name
	l.mu.Unlock()
}

func SetJournald(on bool) { Default.SetJournald(on) }
func (l *Logger) SetJournald(on bool) {
	l.mu.Lock()
	l.journald = on
	l.mu.Unlock()
}
func SetLevel(lvl Level) { Default.SetLevel(lvl) }
func (l *Logger) SetLevel(lvl Level) {
	l.mu.Lock()
	l.level = lvl
	l.mu.Unlock()
}

func SetLevelStyle(s string) { Default.SetLevelStyle(s) }
func (l *Logger) SetLevelStyle(s string) {
	l.mu.Lock()
	l.levelStyle = normalizeLevelStyle(s)
	l.mu.Unlock()
}
func SetLocation(loc *time.Location) { Default.SetLocation(loc) }
func (l *Logger) SetLocation(loc *time.Location) {
	l.mu.Lock()
	l.loc = loc
	l.mu.Unlock()
}

func SetLogFilePermission(m string) { Default.SetLogFilePermission(m) }
func (l *Logger) SetLogFilePermission(m string) {
	l.mu.Lock()
	l.fileMode = m
	l.mu.Unlock()
}

func SetLogGroup(g string) { Default.SetLogGroup(g) }
func (l *Logger) SetLogGroup(g string) {
	l.mu.Lock()
	l.logGroup = g
	l.mu.Unlock()
}
func SetLogPath(path string) { Default.SetLogPath(path) }
func (l *Logger) SetLogPath(path string) {
	l.mu.Lock()
	l.logPath = path
	dirMode := l.dirMode
	logUser := l.logUser
	logGroup := l.logGroup
	l.mu.Unlock()
	if path != "" {
		dir := filepath.Dir(path)
		mode := os.FileMode(0750)
		if dirMode != "" {
			if m, err := parseModeString(dirMode); err == nil && m != 0 {
				mode = m
			}
		}
		os.MkdirAll(dir, mode)
		if dirMode != "" {
			if m, err := parseModeString(dirMode); err == nil && m != 0 {
				os.Chmod(dir, m)
			}
		}
		if logUser != "" || logGroup != "" {
			applyOwner(dir, logUser, logGroup)
		}
	}
}

func SetLogPathPermission(m string) { Default.SetLogPathPermission(m) }
func (l *Logger) SetLogPathPermission(m string) {
	l.mu.Lock()
	l.dirMode = m
	l.mu.Unlock()
}

func SetLogType(t LogType) {
	Default.mu.Lock()
	Default.logType = t
	logPath := Default.logPath
	Default.mu.Unlock()
	if logPath == "" && (t == LogFile || t == LogBoth) {
		Default.SetLogPath("/var/log/dbbackup.log")
	}
}
func (l *Logger) SetLogType(t LogType) {
	l.mu.Lock()
	l.logType = t
	l.mu.Unlock()
}
func SetLogUser(u string) { Default.SetLogUser(u) }
func (l *Logger) SetLogUser(u string) {
	l.mu.Lock()
	l.logUser = u
	l.mu.Unlock()
}

func SetPrefix(enabled bool) { Default.SetPrefix(enabled) }
func (l *Logger) SetPrefix(enabled bool) {
	l.mu.Lock()
	l.prefix = enabled
	l.mu.Unlock()
}

func SetPrefixFormat(f string) { Default.SetPrefixFormat(f) }
func (l *Logger) SetPrefixFormat(f string) {
	if f != "" {
		l.mu.Lock()
		l.prefixFmt = f
		l.mu.Unlock()
	}
}
func (l *Logger) SetSession(id string) {
	l.mu.Lock()
	l.session = id
	l.mu.Unlock()
}

func SetSession(id string) { Default.SetSession(id) }
func (l *Logger) SetShowRunID(on bool) {
	l.mu.Lock()
	l.showRunID = on
	l.mu.Unlock()
}

func SetShowRunID(on bool) { Default.SetShowRunID(on) }
func (l *Logger) SetShowSession(on bool) {
	l.mu.Lock()
	l.showSession = on
	l.mu.Unlock()
}

func SetShowSession(on bool) { Default.SetShowSession(on) }
func (l *Logger) SetSizeFormat(s string) {
	l.mu.Lock()
	l.size = s
	l.mu.Unlock()
}

func SetSizeFormat(s string) { Default.SetSizeFormat(s) }
func SetTimeQuoted(on bool)  { Default.SetTimeQuoted(on) }
func (l *Logger) SetTimeQuoted(on bool) {
	l.mu.Lock()
	l.timeQuoted = on
	l.mu.Unlock()
}

func SetTimings(on bool) { Default.SetTimings(on) }
func (l *Logger) SetTimings(on bool) {
	l.mu.Lock()
	l.timings = on
	l.mu.Unlock()
}

func SizeFormat() string { return Default.SizeFormat() }
func (l *Logger) SizeFormat() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.size
}
func (l Level) String() string {
	switch l {
	case LevelTrace:
		return "TRACE"
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	case LevelNone:
		return "NONE"
	}
	return "UNKNOWN"
}

func (l *Logger) Timings() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.timings
}

func TimingsEnabled() bool                                { return Default.Timings() }
func Trace(action, msg string, fields ...any)             { Default.Trace(action, msg, fields...) }
func (l *Logger) Trace(action, msg string, fields ...any) { l.Log(LevelTrace, action, msg, fields...) }

func (l *Logger) Warn(action, msg string, fields ...any) { l.Log(LevelWarn, action, msg, fields...) }

func Warn(action, msg string, fields ...any) { Default.Warn(action, msg, fields...) }

func (l *Logger) WithLevel(level, override Level, action, msg string, fields ...any) {
	l.logAt(level, override, nil, action, msg, fields...)
}

func WithLevel(level, override Level, action, msg string, fields ...any) {
	Default.WithLevel(level, override, action, msg, fields...)
}

func (l *Logger) WithOverrides(level, override Level, colour *bool, action, msg string, fields ...any) {
	l.logAtPath(level, override, colour, "", action, msg, fields...)
}

func WithOverrides(level, override Level, colour *bool, action, msg string, fields ...any) {
	Default.logAtPath(level, override, colour, "", action, msg, fields...)
}

func WithOverridesTo(level, override Level, colour *bool, path, action, msg string, fields ...any) {
	Default.logAtPath(level, override, colour, path, action, msg, fields...)
}

func (l *Logger) WithOverridesTo(level, override Level, colour *bool, path, action, msg string, fields ...any) {
	l.logAtPath(level, override, colour, path, action, msg, fields...)
}

func appendLog(path, line string) {
	snap := Default.snapshot()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	fmt.Fprintln(f, stripColour(line))
	f.Close()
	if snap.fileMode != "" {
		if m, err := parseModeString(snap.fileMode); err == nil && m != 0 {
			os.Chmod(path, m)
		}
	}
	if snap.logUser != "" || snap.logGroup != "" {
		applyOwner(path, snap.logUser, snap.logGroup)
	}
	if snap.dirMode != "" {
		dir := filepath.Dir(path)
		if m, err := parseModeString(snap.dirMode); err == nil && m != 0 {
			os.Chmod(dir, m)
		}
		if snap.logUser != "" || snap.logGroup != "" {
			applyOwner(dir, snap.logUser, snap.logGroup)
		}
	}
}
func applyOwner(path, owner, group string) error {
	if owner == "" && group == "" {
		return nil
	}
	uid := -1
	gid := -1
	if owner != "" {
		u, err := user.Lookup(owner)
		if err != nil {
			return fmt.Errorf("lookup user %s: %w", owner, err)
		}
		uid64, _ := strconv.Atoi(u.Uid)
		uid = uid64
	}
	if group != "" {
		g, err := user.LookupGroup(group)
		if err != nil {
			return fmt.Errorf("lookup group %s: %w", group, err)
		}
		gid64, _ := strconv.Atoi(g.Gid)
		gid = gid64
	}
	return os.Lchown(path, uid, gid)
}
func colourLevel(level Level, line string) string {
	c := levelColour(level)
	if c == "" {
		return line
	}
	token := "[" + level.String() + "]"
	return strings.Replace(line, token, c+token+colOff, 1)
}

func formatValue(v any) string {
	s := fmt.Sprintf("%v", v)
	if unquoted, err := strconv.Unquote(s); err == nil {
		s = unquoted
	}
	if strings.ContainsAny(s, " \t\"=") {
		return strconv.Quote(s)
	}
	return s
}

func isOctal(s string) bool {
	for _, r := range s {
		if r < '0' || r > '7' {
			return false
		}
	}
	return true
}
func levelColour(l Level) string {
	switch l {
	case LevelTrace:
		return colTrace
	case LevelDebug:
		return colDebug
	case LevelError:
		return colError
	case LevelInfo:
		return colInfo
	case LevelWarn:
		return colWarn
	}
	return ""
}

func (l *Logger) logAt(level Level, override Level, colour *bool, action, msg string, fields ...any) {
	l.logAtPath(level, override, colour, "", action, msg, fields...)
}

func (l *Logger) logAtPath(level Level, override Level, colour *bool, path, action, msg string, fields ...any) {
	snap := l.snapshot()
	if override == LevelNone {
		return
	}
	if override != 0 {
		if level < override {
			return
		}
	} else if level < snap.level {
		return
	}
	now := time.Now()
	if snap.loc != nil {
		now = now.In(snap.loc)
	}

	switch snap.format {
	case FormatJSON:
		entry := map[string]any{
			"time":   now.Format(time.RFC3339),
			"level":  strings.ToLower(level.String()),
			"action": action,
			"msg":    msg,
		}
		if snap.showSession && snap.session != "" {
			entry["session"] = snap.session
		}
		if snap.showRunID {
			for i := 0; i < len(fields)-1; i += 2 {
				if key, ok := fields[i].(string); ok && key == "run_id" {
					entry["run_id"] = fields[i+1]
					break
				}
			}
		}
		if snap.job != "" {
			entry["job"] = snap.job
		}
		for i := 0; i < len(fields)-1; i += 2 {
			key, ok := fields[i].(string)
			if !ok || key == "run_id" {
				continue
			}
			entry[key] = fields[i+1]
		}
		b, _ := json.Marshal(entry)
		l.write(snap, level, string(b), string(b), colour, path)

	default:
		var body strings.Builder
		if snap.showSession && snap.session != "" {
			fmt.Fprintf(&body, "session=%s ", snap.session)
		}
		if snap.showRunID {
			for i := 0; i < len(fields)-1; i += 2 {
				if key, ok := fields[i].(string); ok && key == "run_id" {
					fmt.Fprintf(&body, "run_id=%s ", formatValue(fields[i+1]))
					break
				}
			}
		}
		fmt.Fprintf(&body, "action=%s", action)
		if snap.job != "" {
			fmt.Fprintf(&body, " job=%s", snap.job)
		}
		for i := 0; i < len(fields)-1; i += 2 {
			key, ok := fields[i].(string)
			if !ok || key == "run_id" {
				continue
			}
			fmt.Fprintf(&body, " %s=%s", key, formatValue(fields[i+1]))
		}
		fmt.Fprintf(&body, " msg=%s", strconv.Quote(msg))
		bodyText := body.String()

		marker := "[" + level.String() + "] "
		if snap.useKVLevel() {
			marker = "level=" + strings.ToLower(level.String()) + " "
		}
		fileLine := marker + bodyText
		consoleLine := marker + bodyText
		if snap.prefix {
			ts := now.Format(snap.prefixFmt)
			if snap.timeQuoted {
				ts = `time="` + ts + `"`
			}
			fileLine = ts + " " + fileLine
			if !snap.journald {
				consoleLine = ts + " " + consoleLine
			}
		}
		l.write(snap, level, consoleLine, fileLine, colour, path)
	}
}
func normalizeLevelStyle(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case LevelStyleBracket, LevelStyleKV:
		return strings.ToLower(strings.TrimSpace(s))
	default:
		return LevelStyleAuto
	}
}

func parseModeString(modeStr string) (os.FileMode, error) {
	modeStr = strings.TrimSpace(modeStr)
	if modeStr == "" {
		return 0, nil
	}
	if isOctal(modeStr) {
		m, err := strconv.ParseInt(modeStr, 8, 32)
		if err != nil {
			return 0, err
		}
		return os.FileMode(m), nil
	}
	return parseSymbolicMode(modeStr)
}

func parseSymbolicMode(s string) (os.FileMode, error) {
	if len(s) == 9 {
		var m os.FileMode
		for i := 0; i < 3; i++ {
			shift := uint(6 - i*3)
			if s[i*3] == 'r' {
				m |= 4 << shift
			} else if s[i*3] != '-' && s[i*3] != 's' && s[i*3] != 't' {
				return 0, fmt.Errorf("invalid mode %q", s)
			}
			if s[i*3+1] == 'w' {
				m |= 2 << shift
			} else if s[i*3+1] != '-' && s[i*3+1] != 's' && s[i*3+1] != 't' {
				return 0, fmt.Errorf("invalid mode %q", s)
			}
			if s[i*3+2] == 'x' {
				m |= 1 << shift
			} else if s[i*3+2] != '-' && s[i*3+2] != 's' && s[i*3+2] != 't' {
				return 0, fmt.Errorf("invalid mode %q", s)
			}
		}
		return m, nil
	}
	var digits [3]uint
	classes := map[byte]int{'u': 0, 'g': 1, 'o': 2}
	for _, clause := range strings.Split(s, ",") {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		opIdx := strings.IndexAny(clause, "=+-")
		if opIdx <= 0 || (opIdx == len(clause)-1 && clause[opIdx] != '=') {
			return 0, fmt.Errorf("invalid mode %q", s)
		}
		whoStr := clause[:opIdx]
		op := clause[opIdx]
		var perm uint
		for _, r := range clause[opIdx+1:] {
			switch r {
			case 'r':
				perm |= 4
			case 'w':
				perm |= 2
			case 'x':
				perm |= 1
			default:
				return 0, fmt.Errorf("invalid mode %q", s)
			}
		}
		apply := func(c int) {
			switch op {
			case '=':
				digits[c] = perm
			case '+':
				digits[c] |= perm
			case '-':
				digits[c] &^= perm
			}
		}
		if whoStr == "" || strings.ContainsAny(whoStr, "a") {
			for c := range digits {
				apply(c)
			}
		} else {
			for _, w := range []byte(whoStr) {
				c, ok := classes[w]
				if !ok {
					return 0, fmt.Errorf("invalid mode %q", s)
				}
				apply(c)
			}
		}
	}
	return os.FileMode(digits[0]<<6 | digits[1]<<3 | digits[2]), nil
}
func (l *Logger) snapshot() loggerSnapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return loggerSnapshot{
		level:       l.level,
		format:      l.format,
		logType:     l.logType,
		out:         l.out,
		job:         l.job,
		session:     l.session,
		logPath:     l.logPath,
		fileMode:    l.fileMode,
		dirMode:     l.dirMode,
		logUser:     l.logUser,
		logGroup:    l.logGroup,
		colour:      l.colour,
		prefix:      l.prefix,
		prefixFmt:   l.prefixFmt,
		showSession: l.showSession,
		showRunID:   l.showRunID,
		loc:         l.loc,
		journald:    l.journald,
		levelStyle:  l.levelStyle,
		timeQuoted:  l.timeQuoted,
	}
}

func stripColour(s string) string {
	for _, col := range []string{colDebug, colInfo, colWarn, colError, colOff} {
		s = strings.ReplaceAll(s, col, "")
	}
	return s
}
func (l *Logger) useKVLevel() bool {
	switch l.levelStyle {
	case LevelStyleKV:
		return true
	case LevelStyleBracket:
		return false
	default:
		return l.journald
	}
}

func (s loggerSnapshot) useKVLevel() bool {
	switch s.levelStyle {
	case LevelStyleKV:
		return true
	case LevelStyleBracket:
		return false
	default:
		return s.journald
	}
}

func (l *Logger) write(snap loggerSnapshot, level Level, consoleLine, fileLine string, colour *bool, path string) {
	useColour := snap.colour
	if colour != nil {
		useColour = *colour
	}
	if useColour && snap.format == FormatText && snap.logType != LogFile {
		consoleLine = colourLevel(level, consoleLine)
	}
	if snap.logType == LogConsole || snap.logType == LogBoth {
		fmt.Fprintln(snap.out, consoleLine)
	}
	if path != "" {
		appendLog(path, fileLine)
	} else if (snap.logType == LogFile || snap.logType == LogBoth) && snap.logPath != "" {
		appendLog(snap.logPath, fileLine)
	}
}
