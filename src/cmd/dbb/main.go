// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/stats"

	"github.com/nfrastack/db-backup/internal/config"
	"github.com/nfrastack/db-backup/internal/container"
	"github.com/nfrastack/db-backup/internal/license"
	"github.com/nfrastack/db-backup/internal/log"
	"github.com/nfrastack/db-backup/internal/scheduler/runner"
	"github.com/nfrastack/db-backup/internal/storage"
)

var Version = "dev"
var buildDate = "unknown"

var (
	globalConfigPaths   []string
	globalConfigPath    string
	globalContainer     bool
	globalSystemd       bool
	globalLogLevel      string
	globalLogFormat     string
	globalLogType       string
	globalLogPath       string
	globalLogUser       string
	globalLogGroup      string
	globalLogFileMode   string
	globalLogDirMode    string
	globalLogColour     *bool
	globalLogPrefix     *bool
	globalLogPrefixFmt  string
	globalLogSessionID  *bool
	globalLogRunID      *bool
	globalLogTimings    *bool
	globalLogSizeFormat string
	globalLogTimezone   string
	globalLogUTC        *bool
	globalLogLevelStyle string
	globalLogTimeQuoted *bool
	globalProgress      *bool
)

type command struct {
	run       func([]string) int
	desc      string
	supported bool
	feature   string
}

var commands = map[string]command{
	"dump":      {run: cmdDump, desc: "Run a backup and pipe/save it"},
	"restore":   {run: cmdRestore, desc: "Restore a backup (interactive or CLI)", supported: true, feature: "restore"},
	"scheduler": {run: cmdScheduler, desc: "Run the scheduling daemon"},
	"list":      {run: cmdList, desc: "List available backups"},
	"verify":    {run: cmdVerify, desc: "Verify backup integrity"},
	"prune":     {run: cmdPrune, desc: "Prune old backups"},
	"archive":   {run: cmdArchive, desc: "Archive old backups to secondary storage", supported: true, feature: "archive"},
	"maintain":  {run: cmdMaintain, desc: "Run database maintenance", supported: true, feature: "maintenance"},
	"license":   {run: cmdLicense, desc: "Perform license activities", supported: true},
	"stats":     {run: cmdStats, desc: "Decode a usage-stats payload dump"},
	"version":   {run: cmdVersion, desc: "Build details / version check"},
	"help":      {run: cmdHelp, desc: "Yer looking at it"},
}

var commandAliases = map[string]string{
	"backup":   "dump",
	"schedule": "scheduler",
}

var commandGroups = []struct {
	group string
	cmds  []string
}{
	{"Backup", []string{"dump", "restore", "verify"}},
	{"Scheduling", []string{"scheduler"}},
	{"License", []string{"license"}},
	{"Maintenance", []string{"maintain"}},
	{"Retention", []string{"prune", "archive", "list"}},
	{"Other", []string{"stats", "version", "help"}},
}

func chownLogFile(path, userName, groupName string) {
	if userName != "" {
		currentUser := os.Getenv("USER")
		if currentUser == "" {
			currentUser = "root"
		}
		if currentUser != "root" && currentUser != userName {
			fmt.Fprintf(os.Stderr, "WARN: log user %q differs from running user %q - ownership will likely fail\n", userName, currentUser)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: log open %s: %v\n", path, err)
		return
	}
	f.Close()

	uid, gid, err := storage.LookupUserGroup(userName, groupName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: log chown %s: %v\n", path, err)
		return
	}
	if userName == "" {
		uid = -1
	}
	if groupName == "" {
		gid = -1
	}
	if err := os.Lchown(path, uid, gid); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: log chown %s: %v\n", path, err)
	}
}

func discoverConfig() string {
	for _, p := range []string{
		"db-backup.yaml",
		"db-backup.yml",
		"dbbackup.yaml",
		"dbbackup.yml",
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, p := range config.ConfigPaths() {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func hasCommercialNotice() bool {
	return buildEdition == "supporter" && runtimeMode() == "supporter"
}
func main() {
	invocation := os.Getenv("INVOCATION_ID") != ""
	journal := os.Getenv("JOURNAL_STREAM") != ""
	globalSystemd = invocation || journal

	runner.SetVersion(Version)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: dbb <command> [flags] [args]

Commands:
`)
		for _, g := range commandGroups {
			fmt.Fprintf(os.Stderr, "  %s\n", g.group)
			for _, name := range g.cmds {
				desc := commands[name].desc
				if commandSupported(commands[name]) {
					desc += " *"
				}
				fmt.Fprintf(os.Stderr, "    %-12s %s\n", name, desc)
			}
			fmt.Fprintln(os.Stderr)
		}
		if anyCommandSupported() {
			fmt.Fprintf(os.Stderr, "* Supporter feature\n")
		}
		fmt.Fprintf(os.Stderr, "^ Application default\n\n")
		fmt.Fprintf(os.Stderr, `Global flags:
  --config <path>           Config file path (YAML)
  --log-level <level>       Log level (debug|^info|warn|error|none)
  --log-format <format>     Log format (^text|json)
  --log-type <type>         Log type (^console|file|both)
  --log-path <path>         Log file path (for type file|both)
  --log-user <user>         Change ownership of the log file to this user
  --log-group <grp>         Change ownership of the log file to this group
  --log-colour <false>      Enable coloured log output (^true|false)
  --log-prefix <false>      Include timestamp prefix (^true|false)
  --log-prefix-format       Set the timestamp format (^Go layout 2006-01-02 15:04:05)
  --log-session-id <false>  Include session id on each log line (^true|false)
  --log-run-id <false>      Include per-run id on job log lines (^true|false)
  --log-timings <false>     Include duration fields (total=, dump=, etc.) in logs (^true|false)
  --log-size-format <fmt>   Format of the size= field in job logs (^bytes|bytes_human|kb|...|tb_human)
  --log-timezone <zone>     Timezone for timestamps (eg America/Vancouver)
  --log-utc <false>         Force timestamps to UTC (^false|true)
  --log-level-style <style> Level marker style: ^auto|bracket|kv (auto = kv under systemd, else [LEVEL])
  --log-time-quoted <false> Wrap the timestamp prefix in time="..." (true|^false)
  --progress/--no-progress  Show live backup progress (auto on terminal)
  --dry-run                 Simulate without doing
`)
	}

	containerFlag := flag.Bool("container", false, "Force container mode")
	systemdFlag := flag.Bool("systemd", false, "Force systemd mode")
	flag.Lookup("container").Usage = ""
	flag.Lookup("systemd").Usage = ""

	args := os.Args[1:]
	var scanSkip bool
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() string {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}

		name, inlineVal := a, ""
		hasInline := false
		if j := strings.IndexByte(a, '='); j > 0 && strings.HasPrefix(a, "-") {
			name, inlineVal, hasInline = a[:j], a[j+1:], true
		}
		val := inlineVal
		if !hasInline {
			val = next()
		}
		switch {
		case name == "-container" || name == "--container":
			globalContainer = true
		case name == "-systemd" || name == "--systemd":
			globalSystemd = true
		case name == "-config" || name == "--config" || name == "-c":
			if val != "" && !strings.HasPrefix(val, "-") {
				globalConfigPaths = append(globalConfigPaths, val)
				scanSkip = hasInline == false
			}
		case name == "-progress" || name == "--progress":
			b := true
			if val == "false" || val == "0" {
				b = false
			}
			globalProgress = &b
			scanSkip = hasInline == false
		case name == "-no-progress" || name == "--no-progress":
			b := false
			globalProgress = &b
			scanSkip = hasInline == false
		case name == "-log-level" || name == "--log-level":
			if val != "" {
				globalLogLevel = val
				scanSkip = hasInline == false
			}
		case name == "-log-format" || name == "--log-format":
			if val != "" {
				globalLogFormat = val
				scanSkip = hasInline == false
			}
		case name == "-log-type" || name == "--log-type":
			if val != "" {
				globalLogType = val
				scanSkip = hasInline == false
			}
		case name == "-log-path" || name == "--log-path":
			if val != "" {
				globalLogPath = val
				scanSkip = hasInline == false
			}
		case name == "-log-user" || name == "--log-user":
			if val != "" {
				globalLogUser = val
				scanSkip = hasInline == false
			}
		case name == "-log-group" || name == "--log-group":
			if val != "" {
				globalLogGroup = val
				scanSkip = hasInline == false
			}
		case name == "-log-timezone" || name == "--log-timezone":
			if val != "" {
				globalLogTimezone = val
				scanSkip = hasInline == false
			}
		case name == "-log-utc" || name == "--log-utc":
			b := true
			if val == "false" || val == "0" {
				b = false
			}
			globalLogUTC = &b
			scanSkip = hasInline == false
		case name == "-log-colour" || name == "--log-colour" || name == "-log-color" || name == "--log-color":
			b := true
			if val == "false" || val == "0" {
				b = false
			}
			globalLogColour = &b
			scanSkip = hasInline == false
		case name == "-log-prefix" || name == "--log-prefix":
			b := true
			if val == "false" || val == "0" {
				b = false
			}
			globalLogPrefix = &b
			scanSkip = hasInline == false
		case name == "-log-session-id" || name == "--log-session-id":
			b := true
			if val == "false" || val == "0" {
				b = false
			}
			globalLogSessionID = &b
			scanSkip = hasInline == false
		case name == "-log-run-id" || name == "--log-run-id":
			b := true
			if val == "false" || val == "0" {
				b = false
			}
			globalLogRunID = &b
			scanSkip = hasInline == false
		case name == "-log-timings" || name == "--log-timings":
			b := true
			if val == "false" || val == "0" {
				b = false
			}
			globalLogTimings = &b
			scanSkip = hasInline == false
		case name == "-log-size-format" || name == "--log-size-format":
			if val != "" {
				globalLogSizeFormat = val
				scanSkip = hasInline == false
			}
		case name == "-log-prefix-format" || name == "--log-prefix-format":
			if val != "" {
				globalLogPrefixFmt = val
				scanSkip = hasInline == false
			}
		case name == "-log-level-style" || name == "--log-level-style":
			if val != "" {
				globalLogLevelStyle = val
				scanSkip = hasInline == false
			}
		case name == "-log-time-quoted" || name == "--log-time-quoted":
			b := true
			if val == "false" || val == "0" {
				b = false
			}
			globalLogTimeQuoted = &b
			scanSkip = hasInline == false
		}
		if scanSkip {
			i++
			scanSkip = false
		}
	}

	cp := flag.String("config", "", "Config file path")
	flag.StringVar(cp, "c", "", "Config file path (shorthand)")
	logLevel := flag.String("log-level", "info", "Log level (debug|info|warn|error|none)")
	logFormat := flag.String("log-format", "text", "Log format (text|json)")
	logType := flag.String("log-type", "", "Log type (console|file|both)")
	logPath := flag.String("log-path", "", "Log file path (for log type file|both)")
	logUser := flag.String("log-user", "", "chown the log file to this user")
	logGroup := flag.String("log-group", "", "chown the log file to this group")
	logFileMode := flag.String("log-file-permission", "", "Log file mode (octal 600 or symbolic u=rw,g=r)")
	logDirMode := flag.String("log-path-permission", "", "Log directory mode (octal 750 or symbolic)")
	logColour := flag.Bool("log-colour", true, "Enable coloured log output")
	logPrefix := flag.Bool("log-prefix", true, "Include timestamp prefix in log lines")
	logPrefixFmt := flag.String("log-prefix-format", "2006-01-02 15:04:05", "Timestamp format for the log prefix")
	logSessionID := flag.Bool("log-session-id", true, "Include the session id on each log line")
	logRunID := flag.Bool("log-run-id", true, "Include the per-run id on job log lines")
	logTimings := flag.Bool("log-timings", true, "Include duration fields (total=, dump=, etc.) in logs")
	logSizeFmt := flag.String("log-size-format", "bytes", "Format of the size= field in job logs (bytes|bytes_human|kb|kb_human|mb|mb_human|gb|gb_human|tb|tb_human)")
	logTimezone := flag.String("log-timezone", "", "Timezone for timestamps (eg America/Vancouver)")
	logUTC := flag.Bool("log-utc", false, "Force timestamps to UTC")
	logLevelStyle := flag.String("log-level-style", "auto", "Level marker style: auto|bracket|kv (auto = kv under systemd, else [LEVEL])")
	logTimeQuoted := flag.Bool("log-time-quoted", false, "Wrap the timestamp prefix in time=\"...\"")
	dryRun := flag.Bool("dry-run", false, "Simulate without doing")
	progress := flag.Bool("progress", false, "Show live backup progress (bytes, rate, elapsed)")

	flag.Parse()

	if *cp != "" {
		globalConfigPaths = append(globalConfigPaths, *cp)
	}
	globalContainer = globalContainer || *containerFlag
	if !globalContainer {
		if ok, reason := container.Detect(); ok {
			globalContainer = true
			log.Debug("startup", "container auto-detected", "reason", reason)
		}
	}
	globalSystemd = globalSystemd || *systemdFlag
	log.SetJournald(globalSystemd)
	if globalProgress == nil && *progress {
		b := true
		globalProgress = &b
	}

	if len(globalConfigPaths) == 0 {
		if d := discoverConfig(); d != "" {
			globalConfigPaths = append(globalConfigPaths, d)
		}
	}
	seen := make(map[string]bool, len(globalConfigPaths))
	unique := globalConfigPaths[:0]
	for _, p := range globalConfigPaths {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		unique = append(unique, p)
	}
	globalConfigPaths = unique
	if len(globalConfigPaths) > 0 {
		globalConfigPath = globalConfigPaths[len(globalConfigPaths)-1]
	}

	var cfgLog *config.LogConfig
	if len(globalConfigPaths) > 0 {
		if lc, err := config.LoadLogConfig(globalConfigPaths...); err == nil {
			cfgLog = lc
		}
	}

	if len(globalConfigPaths) > 0 {
		if lc, err := config.LoadLicenseConfig(globalConfigPaths...); err == nil && lc != nil {
			if lc.File != "" {
				license.SetLicenseFile(lc.File)
			}
		}
	}

	var stateDir, tempDir string
	if len(globalConfigPaths) > 0 {
		if sc, err := config.LoadStartupConfig(globalConfigPaths...); err == nil && sc != nil {
			if sc.State != nil {
				stateDir = sc.State.Dir
			}
			tempDir = sc.TempDir
		}
	}

	globalStateDir := config.ResolveStateDir(stateDir)
	license.SetStateDir(globalStateDir)

	globalTempDir := config.ResolveTempDir(tempDir)
	if err := os.MkdirAll(globalTempDir, 0o700); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: cannot create temp dir %s: %v\n", globalTempDir, err)
	} else {
		storage.SetTempDir(globalTempDir)
	}

	if globalLogLevel == "" {
		globalLogLevel = *logLevel
		if cfgLog != nil && cfgLog.Level != "" {
			globalLogLevel = cfgLog.Level
		}
	}
	if globalLogFormat == "" {
		globalLogFormat = *logFormat
		if cfgLog != nil && cfgLog.Format != "" {
			globalLogFormat = cfgLog.Format
		}
	}
	if globalLogType == "" {
		globalLogType = *logType
		if cfgLog != nil && cfgLog.Type != "" {
			globalLogType = cfgLog.Type
		}
	}
	if globalLogPath == "" {
		globalLogPath = *logPath
		if cfgLog != nil && cfgLog.Path != "" {
			globalLogPath = cfgLog.Path
		}
	}
	if globalLogUser == "" {
		globalLogUser = *logUser
		if cfgLog != nil && cfgLog.User != "" {
			globalLogUser = cfgLog.User
		}
	}
	if globalLogGroup == "" {
		globalLogGroup = *logGroup
		if cfgLog != nil && cfgLog.Group != "" {
			globalLogGroup = cfgLog.Group
		}
	}
	if globalLogFileMode == "" {
		globalLogFileMode = *logFileMode
		if cfgLog != nil && cfgLog.FileMode != "" {
			globalLogFileMode = cfgLog.FileMode
		}
	}
	if globalLogDirMode == "" {
		globalLogDirMode = *logDirMode
		if cfgLog != nil && cfgLog.DirMode != "" {
			globalLogDirMode = cfgLog.DirMode
		}
	}
	if globalLogFileMode != "" {
		log.SetLogFilePermission(globalLogFileMode)
	}
	if globalLogDirMode != "" {
		log.SetLogPathPermission(globalLogDirMode)
	}
	if globalLogUser != "" {
		log.SetLogUser(globalLogUser)
	}
	if globalLogGroup != "" {
		log.SetLogGroup(globalLogGroup)
	}
	var fmtFlag log.Format
	if strings.ToLower(globalLogFormat) == "json" {
		fmtFlag = log.FormatJSON
	}
	log.Init(globalLogLevel, globalLogType, fmtFlag)

	logTypeLower := strings.ToLower(globalLogType)
	if globalLogPath != "" && (logTypeLower == "file" || logTypeLower == "both") {
		log.SetLogPath(globalLogPath)
	}
	if (globalLogUser != "" || globalLogGroup != "") && globalLogPath != "" {
		if logTypeLower == "file" || logTypeLower == "both" {
			chownLogFile(globalLogPath, globalLogUser, globalLogGroup)
		} else {
			fmt.Fprintf(os.Stderr, "WARN: log user/group set but log type is %q - nothing to chown\n", globalLogType)
		}
	} else if (globalLogUser != "" || globalLogGroup != "") && globalLogPath == "" {
		fmt.Fprintf(os.Stderr, "WARN: log user/group set but no log path - set log.type to file/both and log.path\n")
	}
	colour := *logColour
	if globalLogColour != nil {
		colour = *globalLogColour
	} else if cfgLog != nil && cfgLog.Colour != nil {
		colour = *cfgLog.Colour
	}
	prefix := *logPrefix
	if globalLogPrefix != nil {
		prefix = *globalLogPrefix
	} else if cfgLog != nil && cfgLog.Prefix != nil {
		prefix = *cfgLog.Prefix
	}
	pf := *logPrefixFmt
	if globalLogPrefixFmt != "" {
		pf = globalLogPrefixFmt
	} else if cfgLog != nil && cfgLog.PrefixFormat != "" {
		pf = cfgLog.PrefixFormat
	}
	log.SetColour(colour)
	log.SetPrefix(prefix)
	log.SetPrefixFormat(pf)

	if globalSystemd {
		explicitPrefix := (globalLogPrefix != nil && *globalLogPrefix) ||
			(cfgLog != nil && cfgLog.Prefix != nil && *cfgLog.Prefix)
		if explicitPrefix {
			log.Warn("startup", "timestamp prefix suppressed on the systemd journal stream")
		}
	}

	showSession := *logSessionID
	if globalLogSessionID != nil {
		showSession = *globalLogSessionID
	} else if cfgLog != nil && cfgLog.SessionID != nil {
		showSession = *cfgLog.SessionID
	}
	showRunID := *logRunID
	if globalLogRunID != nil {
		showRunID = *globalLogRunID
	} else if cfgLog != nil && cfgLog.RunID != nil {
		showRunID = *cfgLog.RunID
	}
	showTimings := *logTimings
	if globalLogTimings != nil {
		showTimings = *globalLogTimings
	} else if cfgLog != nil && cfgLog.Timings != nil {
		showTimings = *cfgLog.Timings
	}
	log.SetShowSession(showSession)
	log.SetShowRunID(showRunID)
	log.SetTimings(showTimings)

	sizeFmt := *logSizeFmt
	if globalLogSizeFormat != "" {
		sizeFmt = globalLogSizeFormat
	} else if cfgLog != nil && cfgLog.SizeFormat != "" {
		sizeFmt = cfgLog.SizeFormat
	}
	log.SetSizeFormat(sizeFmt)

	zone := globalLogTimezone
	if zone == "" {
		zone = *logTimezone
		if cfgLog != nil && cfgLog.Timezone != "" {
			zone = cfgLog.Timezone
		}
	}
	utc := false
	if globalLogUTC != nil {
		utc = *globalLogUTC
	} else if *logUTC {
		utc = true
	} else if cfgLog != nil && cfgLog.UTC != nil {
		utc = *cfgLog.UTC
	}
	log.SetLocation(log.ResolveLocation(zone, utc))
	runner.FilenameLoc = log.ResolveLocation(zone, false)

	levelStyle := *logLevelStyle
	if globalLogLevelStyle != "" {
		levelStyle = globalLogLevelStyle
	} else if cfgLog != nil && cfgLog.LevelStyle != "" {
		levelStyle = cfgLog.LevelStyle
	}
	log.SetLevelStyle(levelStyle)

	timeQuoted := *logTimeQuoted
	if globalLogTimeQuoted != nil {
		timeQuoted = *globalLogTimeQuoted
	} else if cfgLog != nil && cfgLog.TimeQuoted != nil {
		timeQuoted = *cfgLog.TimeQuoted
	}
	log.SetTimeQuoted(timeQuoted)

	statsMgr, statsTracker = stats.Setup(globalConfigPaths, stats.SharedKey(), globalContainer, globalStateDir)
	startEditionBackgroundServices()
	setupLicenseWatch()

	if !globalContainer && !globalSystemd {
		if len(flag.Args()) < 1 {
			printBanner()
		} else {
			cmd := flag.Arg(0)
			if canonical, ok := commandAliases[cmd]; ok {
				cmd = canonical
			}
			if cmd != "scheduler" && cmd != "version" && cmd != "license" {
				printBanner()
			}
		}
	}

	if len(flag.Args()) < 1 {
		flag.Usage()
		os.Exit(1)
	}

	cmd := flag.Arg(0)
	if canonical, ok := commandAliases[cmd]; ok {
		cmd = canonical
	}
	cmdArgs := flag.Args()[1:]

	var filtered []string
	skipNext := false
	for _, a := range cmdArgs {
		if skipNext {
			skipNext = false
			continue
		}
		name, inlineVal := a, ""
		hasInline := false
		if j := strings.IndexByte(a, '='); j > 0 && strings.HasPrefix(a, "-") {
			name, inlineVal, hasInline = a[:j], a[j+1:], true
		}
		_ = inlineVal
		switch name {
		case "--container", "-container", "--systemd", "-systemd":
			continue
		case "--log-colour", "-log-colour", "--log-color", "-log-color",
			"--log-prefix", "-log-prefix",
			"--log-session-id", "-log-session-id",
			"--log-run-id", "-log-run-id",
			"--log-timings", "-log-timings",
			"--progress", "-progress", "--no-progress", "-no-progress":
			continue
		case "--log-level", "-log-level", "--log-format", "-log-format",
			"--log-type", "-log-type", "--log-prefix-format", "-log-prefix-format",
			"--log-size-format", "-log-size-format",
			"--log-timezone", "-log-timezone", "--log-file-permission", "-log-file-permission",
			"--log-path-permission", "-log-path-permission", "--log-path", "-log-path",
			"--log-user", "-log-user", "--log-group", "-log-group",
			"--config", "-config", "-c":
			if !hasInline {
				skipNext = true
			}
			continue
		case "--log-utc", "-log-utc":
			continue
		}
		filtered = append(filtered, a)
	}

	_ = logLevel
	_ = logFormat
	runner.SetDryRun(*dryRun)

	if c, ok := commands[cmd]; ok {
		start := time.Now()
		code := c.run(filtered)
		if op, ok := map[string]string{
			"restore": "restore", "maintain": "maintenance",
			"prune": "prune", "archive": "archive", "verify": "verify",
		}[cmd]; ok {
			stats.Record(op, stats.TriggerManual, "", code == 0, time.Since(start).Milliseconds(), 1)
		}
		os.Exit(code)
	} else {
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		flag.Usage()
		os.Exit(1)
	}
}

func printBanner() {
	if globalContainer || globalSystemd || !isTerminal(os.Stdout) {
		return
	}
	printFullBanner()
}

func printFullBanner() {
	fmt.Println()
	fmt.Println("             .o88o.                                 .                       oooo")
	fmt.Println("             888 `\"                               .o8                       `888")
	fmt.Println("ooo. .oo.   o888oo  oooo d8b  .oooo.    .oooo.o .o888oo  .oooo.    .ooooo.   888  oooo")
	fmt.Println("`888P\"Y88b   888    `888\"\"8P `P  )88b  d88(  \"8   888   `P  )88b  d88' `\"Y8  888 .8P'")
	fmt.Println(" 888   888   888     888      .oP\"888  `\"Y88b.    888    .oP\"888  888        888888.")
	fmt.Println(" 888   888   888     888     d8(  888  o.  )88b   888 . d8(  888  888   .o8  888 `88b.")
	fmt.Println("o888o o888o o888o   d888b    `Y888\"\"8o 8\"\"888P'   \"888\" `Y888\"\"8o `Y8bod8P' o888o o888o")
	fmt.Println()
	fmt.Printf("db-backup %s | build=%s mode=%s | © 2026 Nfrastack https://nfrastack.com\n", Version, buildEdition, runtimeMode())
	fmt.Println()
	fmt.Println("For implementation support and consulting visit: https://nfrastack.com/db-backup")
	fmt.Println()
}

func startupLog() string {
	if globalConfigPath != "" {
		return fmt.Sprintf("config loaded: %s", globalConfigPath)
	}
	return "config loaded: <none>"
}
