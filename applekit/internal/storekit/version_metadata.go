package storekit

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/hyperion-cs/appleseum/applekit/internal/bundle"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

const (
	metadataRangeBudgetBytes = 16_777_216
	metadataTailBytes        = 66_560
	maxVersionPlistBytes     = 1_048_576
	zipLocalHeaderBytes      = 30
	zipMaxFieldBytes         = 65_535
	zipDescriptorBytes       = 24
)

type VersionMetadata struct {
	Version   string
	Build     string
	MinimumOs string
}

// VersionInfo reads the selected asset's Info.plist; dispatch metadata can describe a different release.
func (s *Session) VersionInfo(ctx context.Context, appId int64, versionId string, progress func(float64)) (VersionMetadata, error) {
	kb, err := s.kbsync(ctx)
	if err != nil {
		return VersionMetadata{}, err
	}
	item, err := s.resolveDownload(ctx, appId, kb, versionId, false)
	if err != nil {
		return VersionMetadata{}, err
	}
	// Keep the session's HTTPS connection pool across versions.
	client := &http.Client{Transport: s.Client.Transport, Jar: s.Client.Jar}
	return readRemoteVersion(httpx.WithProfile(ctx, httpx.Configurator), client, asString(item["URL"]), progress)
}

type ipaRangeReader struct {
	ctx       context.Context
	client    *http.Client
	url       string
	size      int64
	remaining int64
	start     int64
	cached    []byte
	directory bool
	readAhead int64
}

func (r *ipaRangeReader) fetch(start, end int64) ([]byte, int64, error) {
	n := end - start + 1
	rangeHeader := fmt.Sprintf("bytes=%d-%d", start, end)
	if start < 0 {
		n = -start
		rangeHeader = fmt.Sprintf("bytes=-%d", n)
	}
	if n <= 0 || n > r.remaining {
		return nil, 0, fmt.Errorf("IPA metadata range budget exceeded")
	}
	r.remaining -= n
	req, err := http.NewRequestWithContext(r.ctx, http.MethodGet, r.url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Range", rangeHeader)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", DefaultDownloadUserAgent)
	res, err := r.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	encoding := res.Header.Get("Content-Encoding")
	if res.StatusCode != http.StatusPartialContent || (encoding != "" && encoding != "identity") {
		return nil, 0, fmt.Errorf("IPA range HTTP %d", res.StatusCode)
	}
	var a, b, size int64
	header := res.Header.Get("Content-Range")
	if _, err = fmt.Sscanf(header, "bytes %d-%d/%d", &a, &b, &size); err != nil || header != fmt.Sprintf("bytes %d-%d/%d", a, b, size) || size <= 0 || a < 0 || b < a || b >= size || (r.size > 0 && size != r.size) {
		return nil, 0, fmt.Errorf("invalid IPA content range")
	}
	if start < 0 {
		start = max(0, size-n)
		end = size - 1
	} else {
		end = min(end, size-1)
	}
	if a != start || b != end {
		return nil, 0, fmt.Errorf("unexpected IPA content range")
	}
	n = b - a + 1
	data := make([]byte, n)
	if _, err := io.ReadFull(res.Body, data); err != nil {
		return nil, 0, err
	}
	// Read through EOF so the transport can reuse this connection. Allocate
	// exactly the bounded range, without ReadAll's intermediate buffer growth.
	var extra [1]byte
	if _, err := io.ReadFull(res.Body, extra[:]); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("unexpected IPA range length")
		}
		return nil, 0, err
	}
	return data, size, nil
}

func (r *ipaRangeReader) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if off < 0 {
		return 0, fmt.Errorf("negative IPA offset")
	}
	if off >= r.size {
		return 0, io.EOF
	}
	n := min(int64(len(p)), r.size-off)
	if off < r.start || off+n > r.start+int64(len(r.cached)) {
		end := off + min(max(n, r.readAhead), r.size-off) - 1
		if r.directory {
			// zip.NewReader walks the central directory sequentially. Fetch it
			// once, rather than paying a CDN round trip for every 64 KiB block.
			// fetch rejects oversized directories before making the request.
			end = r.size - 1
		}
		data, _, err := r.fetch(off, end)
		if err != nil {
			return 0, err
		}
		r.start, r.cached = off, data
	}
	copied := copy(p, r.cached[off-r.start:off-r.start+n])
	if copied < len(p) {
		return copied, io.EOF
	}
	return copied, nil
}

func readRemoteVersion(ctx context.Context, client *http.Client, url string, progress func(float64)) (VersionMetadata, error) {
	// Tail → directory → Info.plist: three bounded range requests, regardless of entry order.

	report := func(percent float64) {
		if progress != nil {
			progress(percent)
		}
	}

	r := &ipaRangeReader{ctx: ctx, client: client, url: url, remaining: metadataRangeBudgetBytes}
	tail, size, err := r.fetch(-metadataTailBytes, -1)
	if err != nil {
		return VersionMetadata{}, err
	}
	r.size = size
	r.start, r.cached = size-int64(len(tail)), tail
	report(100.0 / 3)
	r.directory = true
	z, err := zip.NewReader(r, size)
	r.directory = false
	if err != nil {
		return VersionMetadata{}, err
	}
	report(200.0 / 3)
	for _, f := range z.File {
		if bundle.MainName(f.Name) == "" {
			continue
		}
		if f.UncompressedSize64 > maxVersionPlistBytes || f.CompressedSize64 > maxVersionPlistBytes {
			return VersionMetadata{}, fmt.Errorf("IPA Info.plist too large")
		}
		// Include the local header's maximum name/extra lengths and descriptor,
		// so f.Open and its payload/CRC check share a single cached range.
		r.readAhead = zipLocalHeaderBytes + 2*zipMaxFieldBytes + int64(f.CompressedSize64) + zipDescriptorBytes
		body, err := f.Open()
		if err != nil {
			return VersionMetadata{}, err
		}
		data, err := io.ReadAll(io.LimitReader(body, maxVersionPlistBytes+1))
		body.Close()
		if err != nil {
			return VersionMetadata{}, err
		}
		if len(data) > maxVersionPlistBytes {
			return VersionMetadata{}, fmt.Errorf("IPA Info.plist too large")
		}
		metadata, err := decodeVersionPlist(data)
		if err == nil {
			report(100)
		}
		return metadata, err
	}
	return VersionMetadata{}, fmt.Errorf("IPA Info.plist not found")
}

func decodeVersionPlist(data []byte) (VersionMetadata, error) {
	info, err := bundle.Decode(data)
	if err != nil {
		return VersionMetadata{}, err
	}
	if info.DisplayVersion == "" {
		return VersionMetadata{}, fmt.Errorf("IPA has no version")
	}
	return VersionMetadata{Version: info.DisplayVersion, Build: info.Version, MinimumOs: info.MinimumOs}, nil
}
