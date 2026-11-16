package debuglog

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httputil"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
	"golang.org/x/text/transform"
)

const (
	maxHarDecodedBody = 536_870_912
	// Keep formatting bounded; large bodies still stream without a full RAM copy.
	maxHarPrettyBody = 8_388_608
)

type harBody struct {
	file     *os.File
	offset   int64
	size     int64
	wireSize int64
	decoded  bool
	binary   bool
	comment  string
	trailers []harPair
	temps    []*os.File
}

func newHarBody(file *os.File, offset, size int64) *harBody {
	return &harBody{file: file, offset: offset, size: size, wireSize: size}
}

func (b *harBody) reader() *io.SectionReader { return io.NewSectionReader(b.file, b.offset, b.size) }

func (b *harBody) close() {
	for _, f := range b.temps {
		f.Close()
		os.Remove(f.Name())
	}
}

// Spool transformations to private temporary files rather than retaining large
// bodies in RAM. A failed decoder never replaces the original representation.
func (b *harBody) spool(ctx context.Context, reader io.Reader, limit int64) error {
	f, err := os.CreateTemp(filepath.Dir(b.file.Name()), ".har-body-*")
	if err != nil {
		return err
	}
	b.temps = append(b.temps, f)
	if limit > 0 {
		reader = io.LimitReader(reader, limit+1)
	}
	n, err := io.Copy(contextWriter{ctx: ctx, w: f}, reader)
	if err != nil {
		return err
	}
	if limit > 0 && n > limit {
		return fmt.Errorf("decoded body exceeds %d bytes", limit)
	}
	b.file, b.offset, b.size = f, 0, n
	return nil
}

func (b *harBody) decode(ctx context.Context, header http.Header, wireResponse bool) error {
	if b.size == 0 {
		return nil
	}
	if wireResponse && strings.EqualFold(header.Get("Transfer-Encoding"), "chunked") {
		reader := bufio.NewReader(b.reader())
		if err := b.spool(ctx, httputil.NewChunkedReader(reader), 0); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			b.binary, b.comment = true, "Could not decode chunked body; stored as base64: "+err.Error()
			return nil
		}
		b.wireSize = b.size
		if trailers, err := textproto.NewReader(reader).ReadMIMEHeader(); err == nil {
			b.trailers = harHeaders(http.Header(trailers))
		}
	}
	encodings := strings.Split(strings.Join(header.Values("Content-Encoding"), ","), ",")
	var reader io.Reader = b.reader()
	var closers []io.Closer
	defer func() {
		for _, c := range closers {
			c.Close()
		}
	}()
	changed := false
	var decodeErr error
	if len(encodings) > 4 {
		decodeErr = errors.New("more than four content encodings")
	}
	for i := len(encodings) - 1; i >= 0 && decodeErr == nil; i-- {
		encoding := strings.ToLower(strings.TrimSpace(encodings[i]))
		switch encoding {
		case "", "identity":
			continue
		case "gzip", "x-gzip":
			var r *gzip.Reader
			r, decodeErr = gzip.NewReader(reader)
			if decodeErr == nil {
				reader = r
				closers = append(closers, r)
			}
		case "deflate":
			buffer := bufio.NewReader(reader)
			magic, _ := buffer.Peek(2)
			var r io.ReadCloser
			if len(magic) == 2 && magic[0]&15 == 8 && (int(magic[0])*256+int(magic[1]))%31 == 0 {
				r, decodeErr = zlib.NewReader(buffer)
			} else {
				r = flate.NewReader(buffer)
			}
			if decodeErr == nil {
				reader = r
				closers = append(closers, r)
			}
		case "br":
			reader = brotli.NewReader(reader)
		case "zstd":
			var r *zstd.Decoder
			r, decodeErr = zstd.NewReader(reader, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(67_108_864))
			if decodeErr == nil {
				reader = r
				defer r.Close()
			}
		default:
			decodeErr = fmt.Errorf("unsupported Content-Encoding %q", encoding)
		}
		changed = true
	}
	if changed && decodeErr == nil {
		decodeErr = b.spool(ctx, reader, maxHarDecodedBody)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if decodeErr != nil {
		b.binary = true
		b.comment = "Could not decompress body; stored as base64: " + decodeErr.Error()
	} else if changed {
		b.decoded = true
	}
	return nil
}

func (b *harBody) write(ctx context.Context, w io.Writer, contentType string, response bool) error {
	if contentType == "" {
		var prefix [512]byte
		n, _ := b.reader().Read(prefix[:])
		contentType = http.DetectContentType(prefix[:n])
	}
	media, params, _ := mime.ParseMediaType(contentType)
	text := !b.binary && (strings.HasPrefix(media, "text/") || strings.Contains(media, "json") || strings.Contains(media, "xml") || media == "application/x-www-form-urlencoded" || media == "application/javascript")
	// Apple uses application/x-apple-plist for XML as well as binary plists.
	// Sniff the payload rather than base64-encoding readable XML by MIME alone.
	if !b.binary && strings.Contains(media, "plist") && !strings.Contains(media, "binary") {
		var prefix [512]byte
		n, _ := b.reader().Read(prefix[:])
		text = !strings.HasPrefix(string(prefix[:n]), "bplist") && strings.HasPrefix(http.DetectContentType(prefix[:n]), "text/")
	}
	size := b.size
	if text && params["charset"] != "" && !strings.EqualFold(params["charset"], "utf-8") {
		reader, err := charset.NewReaderLabel(params["charset"], b.reader())
		if err == nil {
			err = b.spool(ctx, reader, maxHarDecodedBody)
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			text = false
			b.comment += " Charset conversion failed; bytes preserved as base64."
		}
	}
	if text {
		_, err := io.Copy(contextWriter{ctx: ctx, w: io.Discard}, transform.NewReader(b.reader(), encoding.UTF8Validator))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		text = err == nil
	}
	var reader io.Reader = b.reader()
	if text && strings.Contains(media, "json") && b.size <= maxHarPrettyBody {
		data, err := io.ReadAll(b.reader())
		if err != nil {
			return err
		}
		var pretty bytes.Buffer
		if json.Indent(&pretty, data, "", "  ") == nil {
			reader = &pretty
		}
	}
	metadata := map[string]any{"mimeType": contentType}
	if response {
		metadata["size"] = size
		if b.decoded {
			metadata["compression"] = size - b.wireSize
		}
	}
	if !text && b.size > 0 {
		if response {
			metadata["encoding"] = "base64"
		} else {
			// HAR 1.2 only standardizes encoding for response.content. Keep
			// binary request bytes losslessly in a documented extension.
			metadata["_encoding"] = "base64"
		}
	}
	if b.comment != "" {
		metadata["comment"] = strings.TrimSpace(b.comment)
	}
	if len(b.trailers) > 0 {
		metadata["_trailers"] = b.trailers
	}
	return harObject(w, metadata, "          ", func(w io.Writer) error {
		if _, err := io.WriteString(w, ",\n            \"text\": \""); err != nil {
			return err
		}
		if text {
			if err := writeHarText(w, reader); err != nil {
				return err
			}
		} else {
			encoder := base64.NewEncoder(base64.StdEncoding, w)
			if _, err := io.Copy(encoder, b.reader()); err != nil {
				return err
			}
			if err := encoder.Close(); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, `"`)
		return err
	})
}

// Marshal bounded UTF-8 chunks, retaining a split rune for the next read. The
// surrounding quotes belong to the caller; JSON escaping remains encoding/json's job.
func writeHarText(w io.Writer, reader io.Reader) error {
	buffer := make([]byte, copyBufferBytes)
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	carry := 0
	for {
		n, err := reader.Read(buffer[carry:])
		if err != nil && err != io.EOF {
			return err
		}
		n += carry
		end := n
		if err != io.EOF {
			for i := max(0, n-utf8.UTFMax+1); i < n; i++ {
				if utf8.RuneStart(buffer[i]) && !utf8.FullRune(buffer[i:n]) {
					end = i
					break
				}
			}
		}
		encoded.Reset()
		if encodeErr := encoder.Encode(string(buffer[:end])); encodeErr != nil {
			return encodeErr
		}
		data := encoded.Bytes()
		if _, writeErr := w.Write(data[1 : len(data)-2]); writeErr != nil {
			return writeErr
		}
		if err == io.EOF {
			return nil
		}
		carry = copy(buffer, buffer[end:n])
	}
}
