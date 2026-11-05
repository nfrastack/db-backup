// SPDX-FileCopyrightText: © 2026 Nfrastack <code@nfrastack.com>
//
// SPDX-License-Identifier: AGPL-3.0-or-later

package compress

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/dsnet/compress/bzip2"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

type Type string

const (
	None  Type = "none"
	Gzip  Type = "gz"
	Bzip2 Type = "bz"
	XZip  Type = "xz"
	Zstd  Type = "zstd"
)

const rsyncSegmentSize = 1 << 20

type Options struct {
	Threads   int
	Rsyncable bool
}

type Compressor interface {
	Compress(w io.Writer, level int, opts Options) (io.WriteCloser, error)
	Decompress(r io.Reader) (io.Reader, error)
}

type noneComp struct{}

type nopCloser struct {
	io.Writer
}

type bzipComp struct{}

type gzipComp struct{}

type xzComp struct{}

type zstdComp struct{}

type segmentEncoder func(buf *bytes.Buffer) (io.WriteCloser, error)

type chunkWriter struct {
	out     io.Writer
	makeEnc segmentEncoder
	threads int

	mu     sync.Mutex
	cur    []byte
	seq    uint64
	closed bool

	results chan chunkResult
	slots   chan struct{}
	wg      sync.WaitGroup
	finish  chan error
}

type chunkResult struct {
	seq uint64
	buf []byte
	err error
}

func (nopCloser) Close() error { return nil }

func (w *chunkWriter) Close() error {
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		if len(w.cur) > 0 {
			w.submit(w.cur)
			w.cur = nil
		}
	}
	w.mu.Unlock()
	w.wg.Wait()
	close(w.results)
	return <-w.finish
}
func (c *bzipComp) Compress(w io.Writer, level int, opts Options) (io.WriteCloser, error) {
	if level < 1 || level > 9 {
		level = 3
	}
	bw, err := bzip2.NewWriter(w, &bzip2.WriterConfig{Level: level})
	if err != nil {
		return nil, fmt.Errorf("bzip2: %w", err)
	}
	return bw, nil
}

func (c *gzipComp) Compress(w io.Writer, level int, opts Options) (io.WriteCloser, error) {
	if level < 1 || level > 9 {
		level = 3
	}
	lvl := gzip.DefaultCompression
	if level > 0 {
		lvl = level
	}
	if opts.Rsyncable || opts.Threads > 1 {
		threads := opts.Threads
		if threads < 1 {
			threads = 1
		}
		return newChunkWriter(w, threads, func(buf *bytes.Buffer) (io.WriteCloser, error) {
			return gzip.NewWriterLevel(buf, lvl)
		})
	}
	return gzip.NewWriterLevel(w, lvl)
}

func (c *noneComp) Compress(w io.Writer, level int, opts Options) (io.WriteCloser, error) {
	return nopCloser{w}, nil
}

func (c *xzComp) Compress(w io.Writer, level int, opts Options) (io.WriteCloser, error) {
	return xz.NewWriter(w)
}

func (c *zstdComp) Compress(w io.Writer, level int, opts Options) (io.WriteCloser, error) {
	if level < 1 || level > 19 {
		level = 3
	}
	zlevel := zstd.EncoderLevelFromZstd(level)
	if opts.Rsyncable {
		threads := opts.Threads
		if threads < 1 {
			threads = 1
		}
		return newChunkWriter(w, threads, func(buf *bytes.Buffer) (io.WriteCloser, error) {
			enc, err := zstd.NewWriter(buf, zstd.WithEncoderLevel(zlevel), zstd.WithEncoderConcurrency(1))
			if err != nil {
				return nil, fmt.Errorf("zstd: %w", err)
			}
			return enc, nil
		})
	}
	if opts.Threads > 1 {
		enc, err := zstd.NewWriter(w,
			zstd.WithEncoderLevel(zlevel),
			zstd.WithEncoderConcurrency(opts.Threads),
			zstd.WithConcurrentBlocks(true),
		)
		if err != nil {
			return nil, fmt.Errorf("zstd: %w", err)
		}
		return enc, nil
	}
	if opts.Threads == 1 {
		enc, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zlevel), zstd.WithEncoderConcurrency(1))
		if err != nil {
			return nil, fmt.Errorf("zstd: %w", err)
		}
		return enc, nil
	}
	enc, err := zstd.NewWriter(w, zstd.WithEncoderLevel(zlevel))
	if err != nil {
		return nil, fmt.Errorf("zstd: %w", err)
	}
	return enc, nil
}

func (c *bzipComp) Decompress(r io.Reader) (io.Reader, error) {
	br, err := bzip2.NewReader(r, nil)
	if err != nil {
		return nil, fmt.Errorf("bzip2: %w", err)
	}
	return br, nil
}

func (c *gzipComp) Decompress(r io.Reader) (io.Reader, error) {
	return gzip.NewReader(r)
}
func (c *noneComp) Decompress(r io.Reader) (io.Reader, error) {
	return r, nil
}

func (c *xzComp) Decompress(r io.Reader) (io.Reader, error) {
	return xz.NewReader(r)
}

func (c *zstdComp) Decompress(r io.Reader) (io.Reader, error) {
	dec, err := zstd.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("zstd: %w", err)
	}
	return dec, nil
}

func Extension(t Type) string {
	switch t {
	case Bzip2:
		return ".bz2"
	case Gzip:
		return ".gz"
	case XZip:
		return ".xz"
	case Zstd:
		return ".zst"
	default:
		return ""
	}
}
func FromExtension(ext string) Type {
	switch strings.ToLower(ext) {
	case ".bz", ".bz2":
		return Bzip2
	case ".gz":
		return Gzip
	case ".xz":
		return XZip
	case ".zst", ".zstd":
		return Zstd
	default:
		return None
	}
}

func New(t Type) (Compressor, error) {
	switch t {
	case Bzip2:
		return &bzipComp{}, nil
	case Gzip:
		return &gzipComp{}, nil
	case None:
		return &noneComp{}, nil
	case XZip:
		return &xzComp{}, nil
	case Zstd:
		return &zstdComp{}, nil
	default:
		return nil, fmt.Errorf("unsupported compression: %s", t)
	}
}
func Parse(s string) Type {
	switch strings.ToLower(s) {
	case "bz", "bzip", "bzip2":
		return Bzip2
	case "gz", "gzip":
		return Gzip
	case "xz", "xzip":
		return XZip
	case "zst", "zstd":
		return Zstd
	default:
		return None
	}
}

func (w *chunkWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	n := len(p)
	for len(p) > 0 {
		if len(w.cur) == 0 {
			w.cur = make([]byte, 0, rsyncSegmentSize)
		}
		room := rsyncSegmentSize - len(w.cur)
		take := room
		if take > len(p) {
			take = len(p)
		}
		w.cur = append(w.cur, p[:take]...)
		p = p[take:]
		if len(w.cur) == rsyncSegmentSize {
			w.submit(w.cur)
			w.cur = nil
		}
	}
	return n, nil
}

func (w *chunkWriter) collect() {
	var next uint64
	pending := map[uint64][]byte{}
	var firstErr error
	stopped := false
	for res := range w.results {
		if stopped {
			continue
		}
		if res.err != nil {
			firstErr = res.err
			stopped = true
			continue
		}
		if res.seq == next {
			if _, err := w.out.Write(res.buf); err != nil {
				firstErr = err
				stopped = true
				continue
			}
			next++
			for {
				b, ok := pending[next]
				if !ok {
					break
				}
				if _, err := w.out.Write(b); err != nil {
					firstErr = err
					stopped = true
					break
				}
				delete(pending, next)
				next++
			}
		} else {
			pending[res.seq] = res.buf
		}
	}
	w.finish <- firstErr
}
func newChunkWriter(out io.Writer, threads int, makeEnc segmentEncoder) (io.WriteCloser, error) {
	if threads < 1 {
		threads = 1
	}
	bufSize := threads * 4
	if bufSize < 16 {
		bufSize = 16
	}
	w := &chunkWriter{
		out:     out,
		makeEnc: makeEnc,
		threads: threads,
		results: make(chan chunkResult, bufSize),
		slots:   make(chan struct{}, threads),
		finish:  make(chan error, 1),
	}
	go w.collect()
	return w, nil
}

func (w *chunkWriter) submit(seg []byte) {
	w.wg.Add(1)
	seq := w.seq
	w.seq++
	go func() {
		defer w.wg.Done()
		w.slots <- struct{}{}
		var buf bytes.Buffer
		res := chunkResult{seq: seq}
		enc, err := w.makeEnc(&buf)
		if err != nil {
			res.err = err
		} else {
			if _, werr := enc.Write(seg); werr != nil {
				res.err = werr
			} else if cerr := enc.Close(); cerr != nil {
				res.err = cerr
			}
			res.buf = buf.Bytes()
		}
		<-w.slots
		w.results <- res
	}()
}
