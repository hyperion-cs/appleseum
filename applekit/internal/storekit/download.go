package storekit

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

// Download is a licensed transfer detached from the mutable Apple session.
// Separate prepared transfers may run concurrently, even after ForgetSecrets.
type Download func(context.Context, string, func(DownloadProgress), DownloadOptions) (string, error)

type DownloadOptions struct {
	MaxBytes    int64
	IdleTimeout time.Duration
	BeforeWrite func(total int64) error
	CheckSpace  func() error
}

var ErrDownloadTooLarge = fmt.Errorf("download exceeds size limit")

func (s *Session) Download(ctx context.Context, id int64, versionId, path string, progress func(DownloadProgress)) (string, error) {
	transfer, err := s.PrepareDownload(ctx, id, versionId)
	if err != nil {
		return "", err
	}
	return transfer(ctx, path, progress, DownloadOptions{})
}

// PrepareDownload resolves the license while the caller owns the session.
func (s *Session) PrepareDownload(ctx context.Context, id int64, versionId string) (Download, error) {
	kb, err := s.kbsync(ctx)
	if err != nil {
		return nil, err
	}
	item, err := s.resolveDownload(ctx, id, kb, versionId, versionId != "")
	if err != nil {
		return nil, err
	}
	if versionId == "" {
		_, latest, err := versionIdentifiers(item)
		if err != nil {
			return nil, err
		}
		meta, _ := item["metadata"].(map[string]any)
		// An inferred version list alone cannot identify the returned asset's version.
		if numToString(meta["softwareVersionExternalIdentifier"]) != latest || asString(item["URL"]) == "" || len(extractSinfs(item)) == 0 {
			item, err = s.resolveDownload(ctx, id, kb, latest, true)
			if err != nil {
				return nil, err
			}
		}
	}
	// Preserve the private cookie jar before the session drops its credentials.
	client := &http.Client{Transport: s.Client.Transport, Jar: s.Client.Jar}
	return func(ctx context.Context, path string, progress func(DownloadProgress), options DownloadOptions) (string, error) {
		return (&Session{Client: client}).downloadItemWithOptions(ctx, item, path, progress, options)
	}, nil
}

func (s *Session) downloadItem(ctx context.Context, item map[string]any, outPath string, progress func(DownloadProgress)) (string, error) {
	return s.downloadItemWithOptions(ctx, item, outPath, progress, DownloadOptions{})
}

func (s *Session) downloadItemWithOptions(ctx context.Context, item map[string]any, outPath string, progress func(DownloadProgress), options DownloadOptions) (result string, err error) {
	downloadUrl, _ := item["URL"].(string)
	if downloadUrl == "" {
		return "", fmt.Errorf("download response has no URL")
	}
	md5Expected, _ := item["md5"].(string)

	if err := os.MkdirAll(filepath.Dir(outPath), 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(outPath), ".download-*")
	if err != nil {
		return "", err
	}
	tmp := file.Name()
	defer func() { err = removeDownloadFile(tmp, err) }()
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := s.fetchToFileWithOptions(ctx, downloadUrl, tmp, md5Expected, options, func(n, total int64) {
		if progress != nil {
			progress(DownloadProgress{Received: n, Total: total, Stage: "downloading"})
		}
	}); err != nil {
		return "", err
	}
	if progress != nil {
		progress(DownloadProgress{Stage: "preparing"})
	}
	if err := assembleIpaWithOptions(ctx, tmp, outPath, item, options); err != nil {
		return "", err
	}
	return outPath, nil
}

// Failed downloads are removed; a supplied MD5 is checked while streaming.
func (s *Session) fetchToFile(ctx context.Context, url, path, expectedMd5 string, progress func(int64, int64)) (err error) {
	return s.fetchToFileWithOptions(ctx, url, path, expectedMd5, DownloadOptions{}, progress)
}

func (s *Session) fetchToFileWithOptions(ctx context.Context, url, path, expectedMd5 string, options DownloadOptions, progress func(int64, int64)) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var idle *time.Timer
	if options.IdleTimeout > 0 {
		idle = time.AfterFunc(options.IdleTimeout, cancel)
		defer idle.Stop()
	}
	req, err := http.NewRequestWithContext(httpx.WithProfile(ctx, httpx.Configurator), http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", DefaultDownloadUserAgent)
	// A redirect-following client (the session client suppresses redirects for login).
	client := &http.Client{Transport: s.Client.Transport, Jar: s.Client.Jar}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	if options.MaxBytes > 0 && resp.ContentLength > options.MaxBytes {
		return ErrDownloadTooLarge
	}
	if options.BeforeWrite != nil {
		if err := options.BeforeWrite(resp.ContentLength); err != nil {
			return err
		}
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			err = removeDownloadFile(path, err)
		}
	}()

	var checksum hash.Hash
	var destination io.Writer = f
	if expectedMd5 != "" {
		checksum = md5.New()
		destination = io.MultiWriter(f, checksum)
	}
	pw := &progressWriter{total: resp.ContentLength, last: time.Now(), report: progress}
	if _, err := io.Copy(&downloadWriter{writer: destination, options: options, idle: idle}, io.TeeReader(contextReader{ctx, resp.Body}, pw)); err != nil {
		return fmt.Errorf("write download: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if checksum != nil {
		got := hex.EncodeToString(checksum.Sum(nil))
		if !strings.EqualFold(got, expectedMd5) {
			return fmt.Errorf("download md5 mismatch: expected %s got %s", expectedMd5, got)
		}
	}
	if progress != nil {
		progress(pw.n, pw.total)
	}
	return ctx.Err()
}

type progressWriter struct {
	total  int64
	n      int64
	last   time.Time
	report func(int64, int64)
}

func (p *progressWriter) Write(b []byte) (int, error) {
	p.n += int64(len(b))
	if p.report != nil && time.Since(p.last) > 200*time.Millisecond {
		p.report(p.n, p.total)
		p.last = time.Now()
	}
	return len(b), nil
}

type DownloadProgress struct {
	Received int64
	Total    int64
	Stage    string
}

// Enforce the actual streamed length as well as Content-Length.
type downloadWriter struct {
	writer  io.Writer
	options DownloadOptions
	written int64
	idle    *time.Timer
	checked time.Time
}

func (w *downloadWriter) Write(p []byte) (int, error) {
	if w.options.MaxBytes > 0 && int64(len(p)) > w.options.MaxBytes-w.written {
		return 0, ErrDownloadTooLarge
	}
	if w.options.CheckSpace != nil && time.Since(w.checked) >= time.Second {
		if err := w.options.CheckSpace(); err != nil {
			return 0, err
		}
		w.checked = time.Now()
	}
	n, err := w.writer.Write(p)
	w.written += int64(n)
	if n > 0 && w.idle != nil {
		w.idle.Reset(w.options.IdleTimeout)
	}
	return n, err
}

func removeDownloadFile(path string, failure error) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return failure
	}
	return errors.Join(failure, err)
}
