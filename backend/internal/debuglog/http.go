package debuglog

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/textproto"
	"os"
	"strconv"
	"strings"
)

const (
	debugResponseSeparator = "\r\n\r\n----- RESPONSE -----\r\n"
)

type debugHttpPart struct {
	body   int64
	header textproto.MIMEHeader
	line   string
}

// Capture files can contain HTTP/2 headers and already dechunked Apple bodies,
// as well as final HTTP/1 wire responses from DebugListener. Parse headers only;
// the source capture always remains unchanged.
func debugPart(file *os.File, start, end int64) (debugHttpPart, error) {
	section := io.NewSectionReader(file, start, min(end-start, maxHttpHeaderBytes))
	reader := bufio.NewReader(section)
	parser := textproto.NewReader(reader)
	line, err := parser.ReadLine()
	if err != nil || !strings.Contains(line, "HTTP/") {
		return debugHttpPart{}, fmt.Errorf("not captured HTTP headers")
	}
	header, err := parser.ReadMIMEHeader()
	if err != nil {
		return debugHttpPart{}, err
	}
	position, _ := section.Seek(0, io.SeekCurrent)
	return debugHttpPart{body: start + position - int64(reader.Buffered()), header: header, line: line}, nil
}

func debugResponseOffset(ctx context.Context, file *os.File, request debugHttpPart, size int64) (int64, error) {
	marker := []byte(debugResponseSeparator)
	// Prefer the recorded body length so marker-like bytes inside a request body
	// are not mistaken for the response separator; a chunked body has none.
	length, err := strconv.ParseInt(request.header.Get("Content-Length"), 10, 64)
	if err == nil && length >= 0 && length <= size-request.body {
		at := request.body + length
		buffer := make([]byte, len(marker))
		if _, err := file.ReadAt(buffer, at); err == nil && bytes.Equal(buffer, marker) {
			return at, nil
		}
	}
	reader := io.NewSectionReader(file, request.body, size-request.body)
	buffer := make([]byte, copyBufferBytes+len(marker))
	used, offset := 0, request.body
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := reader.Read(buffer[used:])
		if index := bytes.Index(buffer[:used+n], marker); index >= 0 {
			return offset + int64(index), nil
		}
		if err == io.EOF {
			return size, nil
		}
		if err != nil {
			return 0, err
		}
		keep := min(used+n, len(marker)-1)
		copy(buffer, buffer[used+n-keep:used+n])
		offset += int64(used + n - keep)
		used = keep
	}
}
