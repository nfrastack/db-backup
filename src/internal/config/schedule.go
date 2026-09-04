// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nfrastack/db-backup/internal/log"
	"gopkg.in/yaml.v3"
)

type Schedule struct {
	Cron        string     `yaml:"cron"`
	Interval    int        `yaml:"interval"`
	Begin       string     `yaml:"begin"`
	Time        TimeList   `yaml:"time"`
	Start       string     `yaml:"start"`
	Recurring   string     `yaml:"recurring"`
	Blackout    *Blackouts `yaml:"blackout"`
	Days        DayList    `yaml:"days"`
	ExcludeDays DayList    `yaml:"exclude_days"`
}

var SupporterParseNL = func(phrase string) (*Schedule, bool, error) {
	if looksLikeNL(phrase) {
		return nil, true, fmt.Errorf("natural-language schedule %q requires Supporter license - community supports cron, interval, single HHMM / HH:MM time, +MM begin and basic blackout HHMM window", phrase)
	}
	return nil, false, nil
}

var SupporterSchedule = CommunityScheduleLimits

type Blackout struct {
	Begin string  `yaml:"begin"`
	End   string  `yaml:"end"`
	Days  DayList `yaml:"days"`
}

type Blackouts []Blackout

type DayList struct {
	weekdays map[time.Weekday]bool
	dom      map[int]bool
}

type DayListData struct {
	Weekdays map[time.Weekday]bool
	DOM      map[int]bool
}

var SupporterParseDayList = func(s string) (*DayListData, error) {
	return nil, fmt.Errorf("schedule days requires Supporter license (community supports single HHMM/HH:MM time, cron/interval without day filtering)")
}

var SupporterDayAllowed = func(s *Schedule, t time.Time) bool { return true }

var SupporterFirstWait = func(s *Schedule) (time.Duration, bool) { return 0, false }

var SupporterBlackoutDaySkip = func(b *Blackout, t time.Time) bool { return false }

type TimeList []int

func (s *Schedule) Blocked(t time.Time) bool {
	if s == nil || s.Blackout == nil {
		return false
	}
	for i := range *s.Blackout {
		if (*s.Blackout)[i].Contains(t) {
			return true
		}
	}
	return false
}

func CommunityScheduleLimits(s *Schedule) error {
	if s.Start != "" && s.Recurring != "" {
		return fmt.Errorf("schedule start %q with recurring %q requires Supporter license (community supports cron, interval, single HHMM / HH:MM time, +MM begin and basic blackout)", s.Start, s.Recurring)
	}
	if !s.Days.Empty() {
		return fmt.Errorf("schedule days requires Supporter license (community supports single HHMM/HH:MM time, cron/interval without day filtering)")
	}
	if !s.ExcludeDays.Empty() {
		return fmt.Errorf("schedule exclude_days requires Supporter license")
	}
	if s.Blackout != nil {
		for _, b := range *s.Blackout {
			if !b.Days.Empty() {
				return fmt.Errorf("blackout days requires Supporter license (community blackout is HHMM/HH:MM window only)")
			}
		}
	}
	if len(s.Time) > 1 {
		original := s.Time.String()
		kept := s.Time[0]
		msg := fmt.Sprintf("schedule time list %q has %d entries - multiple schedule times require Supporter license - using first entry %02d:%02d only", original, len(s.Time), kept/60, kept%60)
		fmt.Fprintln(os.Stderr, "WARN: "+msg)
		log.Warn("schedule", msg, "original", original, "kept", fmt.Sprintf("%02d:%02d", kept/60, kept%60))
		s.Time = TimeList{kept}
	}
	return nil
}

func (b *Blackout) Contains(t time.Time) bool {
	if b == nil || b.Begin == "" || b.End == "" {
		return false
	}
	start, ok1 := parseHHMM(b.Begin)
	end, ok2 := parseHHMM(b.End)
	if !ok1 || !ok2 {
		return false
	}
	if SupporterBlackoutDaySkip(b, t) {
		return false
	}
	now := t.Hour()*60 + t.Minute()
	if end < start {

		return now > start || now < end
	}
	if end == start {
		return false
	}
	return now > start && now < end
}
func (s *Schedule) DayAllowed(t time.Time) bool {
	if s == nil {
		return true
	}
	return SupporterDayAllowed(s, t)
}

func (s *Schedule) Describe() string {
	if s == nil {
		return ""
	}

	if s.Start != "" && s.Recurring != "" {
		return fmt.Sprintf("start %s, then %s", s.Start, describeRecurring(s.Recurring))
	}
	switch {
	case s.Start != "":
		return fmt.Sprintf("start %s", s.Start)
	case s.Cron != "":
		return fmt.Sprintf("cron %s", s.Cron)
	case s.Interval > 0:
		if !s.Time.Empty() {
			return fmt.Sprintf("at %s then every %dm", s.Time.String(), s.Interval)
		}
		return fmt.Sprintf("every %dm", s.Interval)
	case s.Begin != "":
		return fmt.Sprintf("begin %s", s.Begin)
	case !s.Time.Empty():
		return fmt.Sprintf("at %s", s.Time.String())
	}
	return ""
}

func (d *DayList) Empty() bool {
	return d == nil || (len(d.weekdays) == 0 && len(d.dom) == 0)
}

func (t *TimeList) Empty() bool { return t == nil || len(*t) == 0 }
func (s *Schedule) FirstWait() (time.Duration, bool) {
	if s == nil {
		return 0, false
	}
	return SupporterFirstWait(s)
}

func (s *Schedule) IsRecurring() bool {
	if s == nil {
		return false
	}
	if s.Recurring != "" {
		return true
	}
	return s.Cron != "" || s.Interval > 0 || !s.Time.Empty()
}

func (d *DayList) Matches(t time.Time) bool {
	if d.Empty() {
		return true
	}
	if d.weekdays[t.Weekday()] {
		return true
	}
	if d.dom[t.Day()] {
		return true
	}
	return false
}

func (t *TimeList) Next(now time.Time) (time.Time, bool) {
	if t.Empty() {
		return time.Time{}, false
	}
	cur := now.Hour()*60 + now.Minute()
	for _, m := range *t {
		if m > cur {
			return time.Date(now.Year(), now.Month(), now.Day(), m/60, m%60, 0, 0, now.Location()), true
		}
	}
	m := (*t)[0]
	return time.Date(now.Year(), now.Month(), now.Day(), m/60, m%60, 0, 0, now.Location()).Add(24 * time.Hour), true
}

func ParseOffset(s string) (time.Duration, bool) {
	if s == "" {
		return 0, false
	}
	t := strings.TrimSpace(s)
	if strings.HasPrefix(t, "+") {
		n, err := strconv.Atoi(strings.TrimPrefix(t, "+"))
		if err != nil || n < 0 {
			return 0, false
		}
		return time.Duration(n) * time.Minute, true
	}
	if len(t) >= 2 {
		unit := t[len(t)-1]
		num := t[:len(t)-1]
		n, err := strconv.Atoi(num)
		if err != nil || n <= 0 {
			return 0, false
		}
		switch unit {
		case 'm':
			return time.Duration(n) * time.Minute, true
		case 'h':
			return time.Duration(n) * time.Hour, true
		case 'd':
			return time.Duration(n) * 24 * time.Hour, true
		}
	}
	return 0, false
}
func (t *TimeList) String() string {
	if t.Empty() {
		return ""
	}
	var parts []string
	for _, m := range *t {
		parts = append(parts, fmt.Sprintf("%02d:%02d", m/60, m%60))
	}
	return strings.Join(parts, ", ")
}
func (s *Schedule) UnmarshalYAML(value *yaml.Node) error {

	var str string
	if err := value.Decode(&str); err == nil {
		return s.parseString(str)
	}

	type alias Schedule
	var a alias
	if err := value.Decode(&a); err != nil {
		return err
	}
	*s = Schedule(a)

	if s.Recurring != "" {
		ns, ok := parseScheduleString(s.Recurring)
		if !ok {
			return fmt.Errorf("cannot parse recurring schedule: %q", s.Recurring)
		}
		s.Cron = ns.Cron
		s.Interval = ns.Interval
		s.Time = ns.Time
	}
	if err := s.enforceCommunityLimits(); err != nil {
		return err
	}
	return nil
}

func (b *Blackouts) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.SequenceNode {
		var list []Blackout
		if err := node.Decode(&list); err != nil {
			return err
		}
		*b = list
		return nil
	}
	var single Blackout
	if err := node.Decode(&single); err != nil {
		return err
	}
	*b = Blackouts{single}
	return nil
}

func (d *DayList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		return d.parse(node.Value)
	case yaml.SequenceNode:
		for _, n := range node.Content {
			if err := d.parse(n.Value); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("days must be a list or comma-separated string")
}

func (t *TimeList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		return t.parse(node.Value)
	case yaml.SequenceNode:
		for _, n := range node.Content {
			if err := t.parse(n.Value); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("time must be a clock time or a list of clock times")
}

func allDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}
func cronFieldMatch(field string, val, min, max int) bool {
	if field == "*" {
		return true
	}
	if strings.Contains(field, ",") {
		for _, p := range strings.Split(field, ",") {
			if cronFieldMatch(p, val, min, max) {
				return true
			}
		}
		return false
	}
	if strings.Contains(field, "/") {
		parts := strings.SplitN(field, "/", 2)
		step, err := strconv.Atoi(parts[1])
		if err != nil || step <= 0 {
			return false
		}
		if parts[0] == "*" {
			return (val-min)%step == 0
		}
		start, err := strconv.Atoi(parts[0])
		if err != nil {
			return false
		}
		return val >= start && (val-start)%step == 0
	}
	if strings.Contains(field, "-") {
		a, b, err := parseRange(field)
		if err != nil {
			return false
		}
		return val >= a && val <= b
	}
	n, err := strconv.Atoi(field)
	if err != nil {
		return false
	}
	return val == n
}

func describeRecurring(recurring string) string {
	ns, ok := parseScheduleString(recurring)
	if !ok {
		return recurring
	}
	return ns.Describe()
}
func (s *Schedule) enforceCommunityLimits() error {
	return SupporterSchedule(s)
}

func isTimeList(s string) bool {
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			return false
		}
		if _, ok := parseClock(tok); !ok {
			return false
		}
	}
	return true
}
func looksLikeNL(phrase string) bool {
	p := strings.ToLower(strings.TrimSpace(phrase))
	if !strings.Contains(p, " ") {
		return false
	}
	for _, signal := range []string{
		"every", "at ", "am", "pm", "daily", "nightly", "then ", " minute",
		" minutes", " hour", " hours", " midnight", " noon", "after ",
	} {
		if strings.Contains(p, signal) {
			return true
		}
	}
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday", "weekday", "weekend"} {
		if strings.Contains(p, d) {
			return true
		}
	}
	return false
}

func (d *DayList) parse(s string) error {
	data, err := SupporterParseDayList(s)
	if err != nil {
		return err
	}
	d.weekdays = data.Weekdays
	d.dom = data.DOM
	return nil
}

func (t *TimeList) parse(s string) error {
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		m, ok := parseClock(tok)
		if !ok {
			return fmt.Errorf("invalid time %q (use HHMM or HH:MM)", tok)
		}
		*t = append(*t, m)
	}
	sort.Ints(*t)
	return nil
}

func parseClock(s string) (int, bool) {
	var h, m int
	switch len(s) {
	case 4:
		if _, err := fmt.Sscanf(s, "%2d%2d", &h, &m); err != nil {
			return 0, false
		}
	case 5:
		if s[2] != ':' {
			return 0, false
		}
		if _, err := fmt.Sscanf(s, "%2d:%2d", &h, &m); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func parseHHMM(s string) (int, bool) {
	s = strings.ReplaceAll(s, ":", "")
	if len(s) != 4 {
		return 0, false
	}
	h, err1 := strconv.Atoi(s[0:2])
	m, err2 := strconv.Atoi(s[2:4])
	if err1 != nil || err2 != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func parseRange(s string) (int, int, error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("not a range")
	}
	a, err1 := strconv.Atoi(parts[0])
	b, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("bad range")
	}
	return a, b, nil
}

func parseScheduleString(str string) (*Schedule, bool) {
	s := &Schedule{}
	if err := s.parseString(str); err != nil {
		return nil, false
	}
	return s, true
}
func (s *Schedule) parseString(str string) error {

	switch {
	case strings.HasPrefix(str, "+"):
		s.Begin = str
		return s.enforceCommunityLimits()
	case len(str) == 4 && allDigits(str):
		var tl TimeList
		if err := tl.parse(str); err != nil {
			return err
		}
		s.Time = tl
		return s.enforceCommunityLimits()
	case isTimeList(str):
		var tl TimeList
		if err := tl.parse(str); err != nil {
			return err
		}
		s.Time = tl
		return s.enforceCommunityLimits()
	case strings.Contains(str, "*"):
		s.Cron = str
		return s.enforceCommunityLimits()
	case allDigits(str):
		s.Interval, _ = strconv.Atoi(str)
		return s.enforceCommunityLimits()
	}

	ns, matched, err := SupporterParseNL(str)
	if err != nil {
		return err
	}
	if matched {
		*s = *ns
		return s.enforceCommunityLimits()
	}

	if strings.Contains(str, " ") {
		s.Cron = str
		return s.enforceCommunityLimits()
	}
	return fmt.Errorf("cannot parse schedule: %q", str)
}
