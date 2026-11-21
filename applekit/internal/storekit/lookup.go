package storekit

import (
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

// Limit lookup batches to keep the comma-separated URL short.
const (
	lookupBatch = 100
	// Absence from a country's public lookup means delisted there, not unowned.
	itunesLookupUrl = "https://itunes.apple.com/lookup"
)

type storeApp struct {
	Id         int64
	Size       *int64
	ArtworkUrl string
}

func lookupStoreApps(ctx context.Context, client *http.Client, country string, ids []int64, progress func(float64)) (map[int64]storeApp, error) {
	if country == "" {
		// Fall back to the US storefront when the account's storefront is not
		// in the known map, so a refresh does not fail outright. Delisted
		// detection may be slightly off for the real country, but the catalog
		// still loads.
		country = "us"
	}
	if client == nil {
		client = httpx.NewClient(nil)
		client.Timeout = 30 * time.Second
	}
	result := make(map[int64]storeApp, len(ids))
	for start := 0; start < len(ids); start += lookupBatch {
		apps, err := lookupBatchApps(ctx, client, country, ids[start:min(start+lookupBatch, len(ids))])
		if err != nil {
			return nil, fmt.Errorf("lookup batch: %w", err)
		}
		for _, app := range apps {
			result[app.Id] = app
		}
		if progress != nil {
			progress(100 * float64(min(start+lookupBatch, len(ids))) / float64(len(ids)))
		}
	}
	if len(ids) == 0 && progress != nil {
		progress(100)
	}
	return result, nil
}

func lookupBatchApps(ctx context.Context, client *http.Client, country string, ids []int64) ([]storeApp, error) {
	csv := make([]string, len(ids))
	for i, id := range ids {
		csv[i] = strconv.FormatInt(id, 10)
	}
	q := url.Values{
		"id":      {strings.Join(csv, ",")},
		"country": {country},
		"entity":  {"software"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, itunesLookupUrl+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("itunes lookup HTTP %d", resp.StatusCode)
	}
	body, err := httpx.ResponseBodyReader(resp, 8_388_608)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	apps, err := readLookup(body, ids)
	if err != nil {
		return nil, fmt.Errorf("decode lookup json: %w", err)
	}
	return apps, nil
}

type lookupRecord struct {
	TrackId    int64           `json:"trackId"`
	Size       json.RawMessage `json:"fileSizeBytes"`
	ArtworkUrl string          `json:"artworkUrl100"`
}

// Retain only three metadata fields per result and a buffer for individual JSON
// values. Disable legacy error prevalidation, which buffers the entire document;
// syntax is still validated while streaming, through EOF.
func readLookup(body io.Reader, ids []int64) ([]storeApp, error) {
	var out struct {
		Results *[]lookupRecord `json:"results"`
	}
	if err := jsonv2.UnmarshalRead(body, &out, json.DefaultOptionsV1(), json.ReportErrorsWithLegacySemantics(false)); err != nil {
		return nil, err
	}
	if out.Results == nil {
		return nil, fmt.Errorf("lookup response has no results array")
	}
	found := make([]storeApp, 0, len(*out.Results))
	requested := make(map[int64]bool, len(ids))
	for _, id := range ids {
		requested[id] = true
	}
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
