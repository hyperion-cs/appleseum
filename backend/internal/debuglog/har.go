package debuglog

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"appleseum/backend/internal/product"
	"appleseum/backend/internal/wire"
)

type debugHarFile struct {
	file  *os.File
	event wire.DebugEvent
}

type harPair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type harCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Path     string `json:"path,omitempty"`
	Domain   string `json:"domain,omitempty"`
	Expires  string `json:"expires,omitempty"`
	HttpOnly bool   `json:"httpOnly,omitempty"`
	Secure   bool   `json:"secure,omitempty"`
}

func harHeaders(header http.Header) []harPair {
	result := []harPair{}
	keys := make([]string, 0, len(header))
	for key := range header {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		for _, value := range header[key] {
			result = append(result, harPair{key, value})
		}
	}
	return result
}

func harCookies(cookies []*http.Cookie) []harCookie {
	result := []harCookie{}
	for _, c := range cookies {
		cookie := harCookie{Name: c.Name, Value: c.Value, Path: c.Path, Domain: c.Domain, HttpOnly: c.HttpOnly, Secure: c.Secure}
		if !c.Expires.IsZero() {
			cookie.Expires = c.Expires.UTC().Format(time.RFC3339Nano)
		}
		result = append(result, cookie)
	}
	return result
}

// Append streamed fields to an ordinary JSON object. Only body strings need
// streaming: neither a large IPA nor an entire multi-entry HAR is held in RAM.
func harObject(w io.Writer, metadata any, prefix string, fields func(io.Writer) error) error {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent(prefix, "  ")
	if err := encoder.Encode(metadata); err != nil {
		return err
	}
	data := bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))
	if _, err := w.Write(bytes.TrimSuffix(data[:len(data)-1], []byte("\n"+prefix))); err != nil {
		return err
	}
	if fields != nil {
		if err := fields(w); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n"+prefix+"}")
	return err
}

func harStart(input debugHarFile) time.Time {
	name := input.file.Name()
	name = strings.TrimSuffix(name[strings.LastIndexAny(name, `/\`)+1:], ".http")
	if id, err := uuid.Parse(strings.TrimPrefix(name, "exchange-")); err == nil && id.Version() == 7 {
		seconds, nanos := id.Time().UnixTime()
		return time.Unix(seconds, nanos).UTC()
	}
	end, _ := time.Parse(time.RFC3339Nano, input.event.At)
	return end.Add(-time.Duration(input.event.DurationMs) * time.Millisecond)
}

func writeDebugHar(ctx context.Context, w io.Writer, files []debugHarFile) error {
	w = contextWriter{ctx: ctx, w: w}
	slices.SortStableFunc(files, func(a, b debugHarFile) int { return harStart(a).Compare(harStart(b)) })
	if _, err := fmt.Fprintf(w, `{
  "log": {
    "version": "1.2",
    "creator": {
      "name": %q,
      "version": %q
    },
    "entries": [
`, product.DisplayName, product.Version); err != nil {
		return err
	}
	for i, input := range files {
		if i > 0 {
			if _, err := io.WriteString(w, ",\n"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, "      "); err != nil {
			return err
		}
		if err := writeHarEntry(ctx, w, input); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n    ]\n  }\n}\n")
	return err
}

func writeHarEntry(ctx context.Context, w io.Writer, input debugHarFile) error {
	info, err := input.file.Stat()
	if err != nil {
		return err
	}
	start := harStart(input)
	elapsed := max(float64(input.event.DurationMs), float64(info.ModTime().Sub(start))/float64(time.Millisecond), 0)
	wait := min(float64(max(input.event.DurationMs, 0)), elapsed)
	entry := map[string]any{
		"startedDateTime": start.Format(time.RFC3339Nano), "time": elapsed, "cache": struct{}{},
		"timings":  map[string]any{"send": 0, "wait": wait, "receive": elapsed - wait, "comment": "Timing phases are estimates."},
		"_eventId": input.event.Id, "_source": input.event.Source, "_fileModified": info.ModTime().UTC().Format(time.RFC3339Nano),
	}
	if input.event.Detail != "" {
		entry["comment"] = input.event.Detail
	}
	request, err := debugPart(input.file, 0, info.Size())
	if err != nil {
		// Older observer fallbacks stored only response bytes. Preserve those
		// without inventing a request URL or silently dropping the entry.
		entry["comment"] = strings.TrimSpace(input.event.Detail + "\nRequest headers could not be parsed; URL unknown.")
		body := newHarBody(input.file, 0, info.Size())
		body.binary = true
		return harObject(w, entry, "      ", func(w io.Writer) error {
			if _, err := io.WriteString(w, ",\n        \"request\": "); err != nil {
				return err
			}
			if err := harObject(w, map[string]any{
				"method":      input.event.Method,
				"url":         "about:blank",
				"httpVersion": "",
				"headers":     []harPair{},
				"cookies":     []harCookie{},
				"queryString": []harPair{},
				"headersSize": -1,
				"bodySize":    -1,
			}, "        ", nil); err != nil {
				return err
			}
			return writeHarResponse(ctx, w, debugHttpPart{}, body, input.event.Status)
		})
	}
	responseAt, err := debugResponseOffset(ctx, input.file, request, info.Size())
	if err != nil {
		return err
	}
	requestBody := newHarBody(input.file, request.body, responseAt-request.body)
	defer requestBody.close()
	if err := requestBody.decode(ctx, http.Header(request.header), false); err != nil {
		return err
	}
	response := debugHttpPart{}
	status := 0
	responseStart := info.Size()
	if responseAt < info.Size() {
		responseStart = responseAt + int64(len(debugResponseSeparator))
		for range 10 {
			part, err := debugPart(input.file, responseStart, info.Size())
			if err != nil {
				break
			}
			_, code, _ := strings.Cut(part.line, " ")
			code, _, _ = strings.Cut(code, " ")
			status, _ = strconv.Atoi(code)
			if status >= 100 && status < 200 && status != 101 {
				if entry["_informationalResponses"] == nil {
					entry["_informationalResponses"] = []any{}
				}
				entry["_informationalResponses"] = append(entry["_informationalResponses"].([]any), map[string]any{"status": status, "headers": harHeaders(http.Header(part.header))})
				responseStart = part.body
				continue
			}
			response, responseStart = part, part.body
			break
		}
	}
	responseBody := newHarBody(input.file, responseStart, info.Size()-responseStart)
	defer responseBody.close()
	if err := responseBody.decode(ctx, http.Header(response.header), input.event.Source != "Apple"); err != nil {
		return err
	}
	if responseAt == info.Size() {
		responseBody.comment = "Response not captured."
	}
	if input.event.ResponseTruncated {
		entry["_truncated"] = true
	}
	return harObject(w, entry, "      ", func(w io.Writer) error {
		version, _, _ := strings.Cut(response.line, " ")
		if err := writeHarRequest(ctx, w, input, request, requestBody, version); err != nil {
			return err
		}
		return writeHarResponse(ctx, w, response, responseBody, status)
	})
}

func writeHarRequest(ctx context.Context, w io.Writer, input debugHarFile, part debugHttpPart, body *harBody, negotiatedVersion string) error {
	method, target, ok := strings.Cut(part.line, " ")
	if !ok {
		return fmt.Errorf("invalid captured request line")
	}
	target, version, _ := strings.Cut(target, " ")
	// Outgoing captures precede ALPN negotiation; the response records the
	// protocol actually used for both directions of that exchange.
	if negotiatedVersion != "" {
		version = negotiatedVersion
	}
	address, err := url.Parse(target)
	if err != nil {
		return err
	}
	comment := ""
	if !address.IsAbs() {
		address.Scheme, address.Host = "http", part.header.Get("Host")
		if input.event.Source == "Apple" || strings.HasSuffix(address.Host, ":"+product.DefaultManifestPort) {
			address.Scheme = "https"
		}
		comment = "URL scheme inferred from capture source and port."
	}
	address.Fragment = ""
	query := []harPair{}
	for _, field := range strings.Split(address.RawQuery, "&") {
		if field == "" {
			continue
		}
		name, value, _ := strings.Cut(field, "=")
		name, _ = url.QueryUnescape(name)
		value, _ = url.QueryUnescape(value)
		query = append(query, harPair{name, value})
	}
	header := http.Header(part.header)
	request := map[string]any{
		"method":      method,
		"url":         address.String(),
		"httpVersion": version,
		"headers":     harHeaders(header),
		"cookies":     harCookies((&http.Request{Header: header}).Cookies()),
		"queryString": query,
		"headersSize": -1,
		"bodySize":    body.wireSize,
	}
	if comment != "" {
		request["comment"] = comment
	}
	if _, err := io.WriteString(w, ",\n        \"request\": "); err != nil {
		return err
	}
	return harObject(w, request, "        ", func(w io.Writer) error {
		if body.size == 0 {
			return nil
		}
		if _, err := io.WriteString(w, ",\n          \"postData\": "); err != nil {
			return err
		}
		return body.write(ctx, w, header.Get("Content-Type"), false)
	})
}

func writeHarResponse(ctx context.Context, w io.Writer, part debugHttpPart, body *harBody, status int) error {
	header := http.Header(part.header)
	version, rest, _ := strings.Cut(part.line, " ")
	_, statusText, _ := strings.Cut(rest, " ")
	if _, err := io.WriteString(w, ",\n        \"response\": "); err != nil {
		return err
	}
	return harObject(w, map[string]any{
		"status":      status,
		"statusText":  statusText,
		"httpVersion": version,
		"headers":     harHeaders(header),
		"cookies":     harCookies((&http.Response{Header: header}).Cookies()),
		"headersSize": -1,
		"bodySize":    body.wireSize,
		"redirectURL": header.Get("Location"),
	}, "        ", func(w io.Writer) error {
		if _, err := io.WriteString(w, ",\n          \"content\": "); err != nil {
			return err
		}
		return body.write(ctx, w, header.Get("Content-Type"), true)
	})
}
