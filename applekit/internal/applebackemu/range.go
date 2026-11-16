package applebackemu

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// RangeAsset serves caller-supplied bytes in 1..maxRequests range reads.
// Its optional steps must form the scenario's tail.
// The client selects ranges and interprets the asset.
func RangeAsset(endpoint string, data []byte, maxRequests int) ([]Step, error) {
	if maxRequests < 1 {
		return nil, fmt.Errorf("applebackemu: positive range request limit required")
	}
	data = bytes.Clone(data)
	steps := make([]Step, maxRequests)
	for i := range steps {
		step := DownloadAsset(endpoint, Response{})
		step.Name = "asset byte range"
		step.Header.Set("Accept-Encoding", "identity")
		step.Optional = i > 0

		step.Respond = func(request Request) (Response, error) {
			a, b, ok := byteRange(request.Header.Get("Range"), int64(len(data)))
			header := http.Header{"Accept-Ranges": {"bytes"}, "Content-Type": {"application/octet-stream"}}
			if !ok {
				header.Set("Content-Range", fmt.Sprintf("bytes */%d", len(data)))
				return Response{StatusCode: http.StatusRequestedRangeNotSatisfiable, Header: header}, nil
			}
			header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a, b, len(data)))
			return Response{StatusCode: http.StatusPartialContent, Header: header, Body: data[a : b+1]}, nil
		}

		steps[i] = step
	}
	return steps, nil
}

func byteRange(header string, size int64) (int64, int64, bool) {
	if size == 0 || !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false
	}
	first, last, ok := strings.Cut(strings.TrimPrefix(header, "bytes="), "-")
	if !ok {
		return 0, 0, false
	}

	number := func(value string) (int64, bool) {
		if value == "" {
			return 0, false
		}
		for _, c := range value {
			if c < '0' || c > '9' {
				return 0, false
			}
		}
		n, err := strconv.ParseInt(value, 10, 64)
		return n, err == nil
	}

	if first == "" {
		n, ok := number(last)
		if !ok || n == 0 {
			return 0, 0, false
		}
		return max(0, size-n), size - 1, true
	}
	a, ok := number(first)
	if !ok || a >= size {
		return 0, 0, false
	}
	if last == "" {
		return a, size - 1, true
	}
	b, ok := number(last)
	if !ok || b < a {
		return 0, 0, false
	}
	return a, min(b, size-1), true
}
