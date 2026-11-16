package storekit

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

// Compare streaming parsing with a buffered decoder.
func bufferedLookup(body io.Reader, ids []int64) ([]storeApp, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}
	var out struct {
		Results *[]lookupRecord `json:"results"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out.Results == nil {
		return nil, fmt.Errorf("missing results")
	}
	requested := make(map[int64]bool, len(ids))
	for _, id := range ids {
		requested[id] = true
	}
	found := make([]storeApp, 0, len(*out.Results))
	for _, record := range *out.Results {
		if !requested[record.TrackId] {
			continue
		}
		app := storeApp{Id: record.TrackId, ArtworkUrl: record.ArtworkUrl}
		var size json.Number
		if json.Unmarshal(record.Size, &size) == nil {
			if n, err := size.Int64(); err == nil && n > 0 {
				app.Size = &n
			}
		}
		found = append(found, app)
	}
	return found, nil
}

func TestStreamingLookupPreservesBufferedSemantics(t *testing.T) {
	for _, input := range []string{
		`{"results":[]}`,
		`{"unknown":{"nested":[true,null,{}]},"results":[{"trackId":1,"fileSizeBytes":"123"},{"trackId":2,"fileSizeBytes":456},{"trackId":3,"fileSizeBytes":"invalid"},{"trackId":4,"fileSizeBytes":null},{"trackId":99}]}`,
		`{"RESULTS":[{"TRACKID":1,"FILESIZEBYTES":"100","ARTWORKURL100":"https://example.test/icon"},null]}`,
		`{"results":[{"trackId":1}],"results":[{"trackId":2}]}`,
		`{}`, `null`, `{"results":null}`, `{"results":{}}`, `{"results":[`,
		`{"results":[]}garbage`, `{"results":[]} {}`, `{"results":[{"trackId":"invalid"}]}`,
	} {
		ids := []int64{1, 2, 3, 4}
		want, oldErr := bufferedLookup(strings.NewReader(input), ids)
		got, err := readLookup(strings.NewReader(input), ids)
		if (err == nil) != (oldErr == nil) || err == nil && !reflect.DeepEqual(got, want) {
			t.Fatalf("input=%s\ngot=%+v error=%v; want=%+v error=%v", input, got, err, want, oldErr)
		}
	}
}

type smallReads struct {
	io.Reader
	maximum int
}

func (r *smallReads) Read(p []byte) (int, error) {
	if len(p) > r.maximum {
		return 0, fmt.Errorf("attempted to buffer an unused field: requested %d bytes", len(p))
	}
	return r.Reader.Read(p)
}

func TestStreamingLookupDoesNotBufferTheWholeDocument(t *testing.T) {
	unused := strings.Repeat(`"`+strings.Repeat("x", 8_192)+`",`, 255) + `"last"`
	input := `{"ignored":[` + unused + `],"results":[{"trackId":1,"description":"` + strings.Repeat("y", 8_192) + `","fileSizeBytes":"42"}]}`
	apps, err := readLookup(&smallReads{Reader: strings.NewReader(input), maximum: 65_536}, []int64{1})
	if err != nil || len(apps) != 1 || apps[0].Size == nil || *apps[0].Size != 42 {
		t.Fatalf("metadata=%+v error=%v", apps, err)
	}
}

func TestStreamingLookupAllowsLargeIndividualValues(t *testing.T) {
	// The standard decoder buffers a single string token, even when skipped.
	input := `{"results":[{"trackId":1,"description":"` + strings.Repeat("x", 2_097_152) + `","fileSizeBytes":"42"}]}`
	apps, err := readLookup(strings.NewReader(input), []int64{1})
	if err != nil || len(apps) != 1 || apps[0].Size == nil || *apps[0].Size != 42 {
		t.Fatalf("metadata=%+v error=%v", apps, err)
	}
}

func TestStreamingLookupValidatesLimitAndGzipTrailer(t *testing.T) {
	for _, mode := range []string{"limit", "gzip trailer"} {
		t.Run(mode, func(t *testing.T) {
			data := []byte(`{"results":[]}` + strings.Repeat(" ", 1_024))
			encoding := "identity"
			if mode == "gzip trailer" {
				var b bytes.Buffer
				writer := gzip.NewWriter(&b)
				writer.Write(data)
				writer.Close()
				data = b.Bytes()
				data[len(data)-8] ^= 1
				encoding = "gzip"
			}
			limit := int64(128)
			if encoding == "gzip" {
				limit = 4_096
			}
			response := &http.Response{Header: http.Header{"Content-Encoding": {encoding}}, Body: io.NopCloser(bytes.NewReader(data))}
			defer response.Body.Close()
			reader, err := httpx.ResponseBodyReader(response, limit)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if apps, err := readLookup(reader, []int64{1}); err == nil || apps != nil {
				t.Fatalf("invalid response accepted: %+v %v", apps, err)
			}
		})
	}
}

func BenchmarkLookupJson(b *testing.B) {
	for _, description := range []int{0, 8_192} {
		b.Run(fmt.Sprintf("description=%d", description), func(b *testing.B) {
			var payload strings.Builder
			payload.WriteString(`{"results":[`)
			ids := make([]int64, lookupBatch)
			for i := range ids {
				ids[i] = int64(i + 1)
				if i != 0 {
					payload.WriteByte(',')
				}
				fmt.Fprintf(&payload, `{"trackId":%d,"fileSizeBytes":"123456789","artworkUrl100":"https://example.test/icon","description":%q}`, i+1, strings.Repeat("x", description))
			}
			payload.WriteString(`]}`)
			input := payload.String()
			for _, parser := range []struct {
				name string
				read func(io.Reader, []int64) ([]storeApp, error)
			}{{"buffered", bufferedLookup}, {"streaming", readLookup}} {
				b.Run(parser.name, func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(len(input)))
					for b.Loop() {
						apps, err := parser.read(strings.NewReader(input), ids)
						if err != nil || len(apps) != len(ids) {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}
