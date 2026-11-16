package httpx

import (
	"compress/gzip"
	"compress/zlib"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
)

// ReadResponseBody bounds the wire body and each decompression layer.
// The caller must close response.Body.
func ReadResponseBody(response *http.Response, limit int64) ([]byte, error) {
	reader, err := ResponseBodyReader(response, limit)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return body, nil
}

// ResponseBodyReader decompresses incrementally, bounding both compressed and
// expanded bytes at every layer. Close releases the decompression resources;
// the caller still owns response.Body and must close it.
// Read to EOF to validate compression trailers and permit connection reuse.
func ResponseBodyReader(response *http.Response, limit int64) (io.ReadCloser, error) {
	if limit <= 0 || limit == math.MaxInt64 {
		return nil, fmt.Errorf("response limit must be between 1 and %d", int64(math.MaxInt64-1))
	}
	stream := &responseReader{Reader: limitResponse(response.Body, limit)}

	fail := func(err error) (io.ReadCloser, error) { stream.Close(); return nil, err }

	encodings := strings.Split(strings.Join(response.Header.Values("Content-Encoding"), ","), ",")
	for i := len(encodings) - 1; i >= 0; i-- {
		var reader io.Reader
		switch encoding := strings.ToLower(strings.TrimSpace(encodings[i])); encoding {
		case "", "identity":
			continue
		case "gzip":
			r, err := gzip.NewReader(stream.Reader)
			if err != nil {
				return fail(fmt.Errorf("decode gzip response: %w", err))
			}
			reader = r
			stream.decoders = append(stream.decoders, r)
		case "deflate":
			r, err := zlib.NewReader(stream.Reader)
			if err != nil {
				return fail(fmt.Errorf("decode deflate response: %w", err))
			}
			reader = r
			stream.decoders = append(stream.decoders, r)
		case "br":
			reader = brotli.NewReader(stream.Reader)
		default:
			return fail(fmt.Errorf("unsupported response encoding %q", encoding))
		}
		stream.sources = append(stream.sources, stream.Reader)
		stream.Reader = limitResponse(reader, limit)
	}
	return stream, nil
}

type responseReader struct {
	io.Reader
	decoders []io.Closer
	sources  []io.Reader
}

func (r *responseReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		// Some decompressors stop before their input's EOF. Consume the rest
		// to enforce every layer's limit and leave the HTTP connection reusable.
		for i := len(r.sources) - 1; i >= 0; i-- {
			if _, drainErr := io.Copy(io.Discard, r.sources[i]); drainErr != nil {
				return n, drainErr
			}
		}
		r.sources = nil
	}
	return n, err
}

func (r *responseReader) Close() error {
	var err error
	for i := len(r.decoders) - 1; i >= 0; i-- {
		err = errors.Join(err, r.decoders[i].Close())
	}
	return err
}

type boundedResponseReader struct {
	reader           io.Reader
	remaining, limit int64
}

func limitResponse(reader io.Reader, limit int64) io.Reader {
	return &boundedResponseReader{reader: reader, remaining: limit, limit: limit}
}

func (r *boundedResponseReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		var extra [1]byte
		n, err := r.reader.Read(extra[:])
		if n != 0 {
			return 0, fmt.Errorf("response exceeds %d bytes", r.limit)
		}
		return 0, err
	}
	if int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= int64(n)
	return n, err
}

type HttpError struct{ StatusCode int }

func (e *HttpError) Error() string { return fmt.Sprintf("Apple returned HTTP %d", e.StatusCode) }
