// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Backend string

const (
	Filesystem Backend = "filesystem"
	S3         Backend = "s3"
	Azure      Backend = "azure"
	GCS        Backend = "gcs"
	WebDAV     Backend = "webdav"
)

type BackendSpec struct {
	Name  string
	Label string
	New   func(opts map[string]string) (Storage, error)
}

var (
	backendsMu sync.RWMutex
	backends   = map[string]BackendSpec{}
)

var (
	spoolDirMu sync.RWMutex
	spoolDir   string
)

func SetTempDir(dir string) {
	spoolDirMu.Lock()
	defer spoolDirMu.Unlock()
	spoolDir = dir
}

func SpoolDir() string {
	spoolDirMu.RLock()
	defer spoolDirMu.RUnlock()
	if spoolDir != "" {
		return spoolDir
	}
	return os.TempDir()
}

type Entry struct {
	Path    string
	Size    int64
	ModTime int64
}

type fsStorage struct {
	basePath string
	fileMode string
	dirMode  string
	owner    string
	group    string
}

type Storage interface {
	Upload(ctx context.Context, path string, r io.Reader) (int64, error)
	Download(ctx context.Context, path string) (io.ReadCloser, int64, error)
	List(ctx context.Context, prefix string) ([]Entry, error)
	Delete(ctx context.Context, path string) error
}

type StorageOpts struct {
	Path     string
	FileMode string
	DirMode  string
	User     string
	Group    string
}

func Backends() []string {
	backendsMu.RLock()
	defer backendsMu.RUnlock()
	names := make([]string, 0, len(backends))
	for name := range backends {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s *fsStorage) Delete(ctx context.Context, path string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return os.Remove(filepath.Join(s.basePath, path))
}

func (s *fsStorage) Download(ctx context.Context, path string) (io.ReadCloser, int64, error) {
	select {
	case <-ctx.Done():
		return nil, 0, ctx.Err()
	default:
	}
	fullPath := filepath.Join(s.basePath, path)
	f, err := os.Open(fullPath)
	if err != nil {
		return nil, 0, fmt.Errorf("open: %w", err)
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, fmt.Errorf("stat: %w", err)
	}
	return f, fi.Size(), nil
}

func (s *fsStorage) List(ctx context.Context, prefix string) ([]Entry, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		if fi, err := os.Stat(filepath.Join(s.basePath, prefix)); err == nil && !fi.IsDir() {
			rel, _ := filepath.Rel(s.basePath, filepath.Join(s.basePath, prefix))
			return []Entry{{Path: rel, Size: fi.Size(), ModTime: fi.ModTime().UnixNano()}}, nil
		}
	}
	searchPath := filepath.Join(s.basePath, prefix)
	if fi, err := os.Stat(searchPath); err != nil {
		if os.IsNotExist(err) {
			if prefix == "" {
				return nil, nil
			}
			var filtered []Entry
			_ = filepath.WalkDir(s.basePath, func(path string, d os.DirEntry, err error) error {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
				}
				if err != nil || d.IsDir() {
					return nil
				}
				rel, _ := filepath.Rel(s.basePath, path)
				if !strings.HasPrefix(rel, prefix) {
					return nil
				}
				fi, err := d.Info()
				if err != nil {
					return nil
				}
				filtered = append(filtered, Entry{Path: rel, Size: fi.Size(), ModTime: fi.ModTime().UnixNano()})
				return nil
			})
			return filtered, nil
		}
		return nil, fmt.Errorf("stat: %w", err)
	} else if !fi.IsDir() {
		rel, _ := filepath.Rel(s.basePath, searchPath)
		return []Entry{{Path: rel, Size: fi.Size(), ModTime: fi.ModTime().UnixNano()}}, nil
	}

	var entries []Entry
	err := filepath.WalkDir(searchPath, func(path string, d os.DirEntry, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(s.basePath, path)
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		entries = append(entries, Entry{Path: rel, Size: fi.Size(), ModTime: fi.ModTime().UnixNano()})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk: %w", err)
	}
	return entries, nil
}
func LookupBackend(name string) *BackendSpec {
	if name == "" {
		return nil
	}
	backendsMu.RLock()
	defer backendsMu.RUnlock()
	spec, ok := backends[name]
	if !ok {
		return nil
	}
	return &spec
}

func LookupUserGroup(userName, groupName string) (uid, gid int, err error) {
	if userName != "" {
		u, e := user.Lookup(userName)
		if e != nil {
			return 0, 0, fmt.Errorf("user %s: %w", userName, e)
		}
		uid, _ = strconv.Atoi(u.Uid)
	}
	if groupName != "" {
		g, e := user.LookupGroup(groupName)
		if e != nil {
			return 0, 0, fmt.Errorf("group %s: %w", groupName, e)
		}
		gid, _ = strconv.Atoi(g.Gid)
	}
	return uid, gid, nil
}
func New(backend Backend, opts map[string]string) (Storage, error) {
	spec := LookupBackend(string(backend))
	if spec == nil {
		return nil, fmt.Errorf("unsupported storage backend: %s", backend)
	}
	return spec.New(opts)
}

func ParseMode(modeStr string) os.FileMode {
	m, err := parseModeString(modeStr)
	if err != nil {
		return 0
	}
	return m
}
func RegisterBackend(spec BackendSpec) {
	if spec.Name == "" {
		panic("storage: backend registered with empty name")
	}
	backendsMu.Lock()
	defer backendsMu.Unlock()
	if _, exists := backends[spec.Name]; exists {
		panic("storage: duplicate backend " + spec.Name)
	}
	backends[spec.Name] = spec
}

func (s *fsStorage) Upload(ctx context.Context, path string, r io.Reader) (int64, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	default:
	}
	fullPath := filepath.Join(s.basePath, path)

	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return 0, fmt.Errorf("mkdir: %w", err)
	}

	tmpFile, err := os.CreateTemp(dir, ".tmp-"+filepath.Base(fullPath)+"-*.part")
	if err != nil {
		return 0, fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmpFile.Name()
	cleaned := false
	defer func() {
		tmpFile.Close()
		if !cleaned {
			os.Remove(tmpPath)
		}
	}()

	n, err := io.Copy(tmpFile, r)
	if err != nil {
		return n, fmt.Errorf("write to storage: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return n, fmt.Errorf("close temp: %w", err)
	}

	if n == 0 {
		os.Remove(tmpPath)
		cleaned = true
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
		return 0, fmt.Errorf("write to storage: no data written (dump failed before any output)")
	}
	cleaned = true
	if err := os.Rename(tmpPath, fullPath); err != nil {
		os.Remove(tmpPath)
		return n, fmt.Errorf("rename: %w", err)
	}

	if s.fileMode != "" {
		mode, err := parseModeString(s.fileMode)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: invalid file_mode %q: %v\n", s.fileMode, err)
		} else {
			os.Chmod(fullPath, mode)
		}
	}
	if s.dirMode != "" {
		mode, err := parseModeString(s.dirMode)
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARN: invalid dir_mode %q: %v\n", s.dirMode, err)
		} else {
			os.Chmod(dir, mode)
		}
	}
	if err := s.applyOwner(fullPath); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: chown %s: %v\n", fullPath, err)
	}
	if err := s.applyOwner(dir); err != nil {
		fmt.Fprintf(os.Stderr, "WARN: chown %s: %v\n", dir, err)
	}

	return n, nil
}
func (s *fsStorage) applyOwner(path string) error {
	if s.owner == "" && s.group == "" {
		return nil
	}

	uid := -1
	gid := -1

	if s.owner != "" {
		u, err := user.Lookup(s.owner)
		if err != nil {
			return fmt.Errorf("lookup user %s: %w", s.owner, err)
		}
		uid64, _ := strconv.Atoi(u.Uid)
		uid = uid64
	}
	if s.group != "" {
		g, err := user.LookupGroup(s.group)
		if err != nil {
			return fmt.Errorf("lookup group %s: %w", s.group, err)
		}
		gid64, _ := strconv.Atoi(g.Gid)
		gid = gid64
	}

	return os.Lchown(path, uid, gid)
}

func init() {
	RegisterBackend(BackendSpec{
		Name:  "filesystem",
		Label: "Filesystem",
		New: func(opts map[string]string) (Storage, error) {
			return &fsStorage{
				basePath: winResolvePath(filepath.Clean(opts["path"])),
				fileMode: opts["file_mode"],
				dirMode:  opts["dir_mode"],
				owner:    opts["user"],
				group:    opts["group"],
			}, nil
		},
	})
}

func isOctal(s string) bool {
	for _, r := range s {
		if r < '0' || r > '7' {
			return false
		}
	}
	return true
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

func winResolvePath(p string) string {
	if runtime.GOOS != "windows" {
		return p
	}
	if p == "" {
		return p
	}
	p = filepath.Clean(p)
	if filepath.VolumeName(p) != "" {
		return p
	}
	if filepath.IsAbs(p) {
		if exe, err := os.Executable(); err == nil {
			if vol := filepath.VolumeName(exe); vol != "" {
				return vol + p
			}
		}
		if sd := os.Getenv("SystemDrive"); sd != "" {
			return sd + p
		}
		// Fallback to current working directory drive
		if wd, err := os.Getwd(); err == nil {
			if vol := filepath.VolumeName(wd); vol != "" {
				return vol + p
			}
		}
	}
	return p
}
