// Package artwork fetches and decodes App Store artwork.
package artwork

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

// MaxBytes bounds downloaded and decoded artwork.
const (
	MaxBytes = 1_048_576
)

// Client reuses its transport and public-service cookies across fetches.
type Client struct{ client *http.Client }

// NewClient creates an independent public CDN session. A nil transport
// selects Apple's TLS profiles; the client never shares account cookies.
func NewClient(transport http.RoundTripper) *Client {
	client := httpx.NewClient(nil)
	if transport != nil {
		client.Transport = transport
	}
	return &Client{client: client}
}

func allowedArtworkUrl(raw string) bool {
	if len(raw) > 4_096 {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.User == nil && (u.Port() == "" || u.Port() == "443") && strings.HasSuffix(strings.ToLower(u.Hostname()), ".mzstatic.com")
}

// Fetch retrieves bounded image bytes from Apple CDN, validating every redirect.
func (c *Client) Fetch(ctx context.Context, raw string) ([]byte, error) {
	if !allowedArtworkUrl(raw) {
		return nil, fmt.Errorf("unsupported artwork address")
	}
	client := *c.client
	client.Timeout = 10 * time.Second

	client.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 4 || !allowedArtworkUrl(r.URL.String()) {
			return fmt.Errorf("unsupported artwork redirect")
		}
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", httpx.DefaultConfiguratorUserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("artwork request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("artwork HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil || len(data) > MaxBytes {
		return nil, fmt.Errorf("artwork read failed")
	}
	return data, nil
}

// CloseIdleConnections releases pooled connections without interrupting fetches.
func (c *Client) CloseIdleConnections() { c.client.CloseIdleConnections() }

// Decode reads PNG (including Apple's optimized PNG) or JPEG artwork.
// It bounds input size and image dimensions, without resizing for a particular UI.
func Decode(data []byte) (image.Image, error) {
	if len(data) > MaxBytes {
		return nil, fmt.Errorf("artwork too large")
	}
	data, err := standardPng(data)
	if err != nil {
		return nil, err
	}
	config, kind, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (kind != "png" && kind != "jpeg") || config.Width <= 0 || config.Height <= 0 || config.Width > 1_024 || config.Height > 1_024 {
		return nil, fmt.Errorf("unsupported artwork")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("invalid artwork")
	}
	return img, nil
}
