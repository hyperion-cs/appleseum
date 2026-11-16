package sap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"

	"howett.net/plist"

	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
	"github.com/hyperion-cs/appleseum/applekit/internal/plistutil"
)

const (
	setupCertificateKey = "sign-sap-setup-cert"
	setupBufferKey      = "sign-sap-setup-buffer"
	maxSetupBody        = int64(1_048_576)
)

type setupProtocol struct {
	client  *http.Client
	headers http.Header
}

func (p setupProtocol) applyHeaders(request *http.Request) {
	request.Header = p.headers.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	if request.Header.Get("User-Agent") == "" {
		request.Header.Set("User-Agent", httpx.UserAgentFor(request.Context()))
	}
	if request.Header.Get("Accept") == "" {
		request.Header.Set("Accept", "*/*")
	}
}

func (p setupProtocol) certificate(ctx context.Context, endpoint string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("create SAP certificate request: %w", err)
	}

	p.applyHeaders(request)

	body, err := p.send(request)
	if err != nil {
		return nil, fmt.Errorf("fetch SAP certificate: %w", err)
	}

	return plistBytes(body, setupCertificateKey)
}

func (p setupProtocol) exchange(ctx context.Context, endpoint string, input []byte) ([]byte, error) {
	envelope, err := plist.Marshal(map[string]any{setupBufferKey: input}, plist.XMLFormat)
	if err != nil {
		return nil, fmt.Errorf("encode SAP setup message: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(envelope))
	if err != nil {
		return nil, fmt.Errorf("create SAP setup request: %w", err)
	}

	p.applyHeaders(request)
	request.Header.Set("Content-Type", "application/x-apple-plist")

	body, err := p.send(request)
	if err != nil {
		return nil, fmt.Errorf("exchange SAP setup message: %w", err)
	}

	return plistBytes(body, setupBufferKey)
}

func (p setupProtocol) send(request *http.Request) ([]byte, error) {
	response, err := p.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send SAP request: %w", err)
	}

	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxSetupBody))

		return nil, &httpx.HttpError{StatusCode: response.StatusCode}
	}

	body, err := httpx.ReadResponseBody(response, maxSetupBody)
	if err != nil {
		return nil, fmt.Errorf("read SAP response: %w", err)
	}

	return body, nil
}

func plistBytes(document []byte, key string) ([]byte, error) {
	var values map[string]any
	if _, err := plistutil.Unmarshal(document, &values); err != nil {
		return nil, fmt.Errorf("decode Apple plist: %w", err)
	}

	value, ok := values[key].([]byte)
	if !ok || len(value) == 0 {
		return nil, errors.New("Apple plist is missing " + key)
	}

	return value, nil
}
