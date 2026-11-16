package applekit

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"howett.net/plist"

	"github.com/hyperion-cs/appleseum/applekit/internal/adi"
	"github.com/hyperion-cs/appleseum/applekit/internal/grandslam"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
	"github.com/hyperion-cs/appleseum/applekit/internal/plistutil"
)

const (
	idmsProvisioningGeneration = 2
)

func provisionIdms(ctx context.Context, client *http.Client, s *installation, options AuthenticatorOptions, at time.Time) error {
	if len(s.Idms) > 0 && s.IdmsGeneration == idmsProvisioningGeneration {
		return nil
	}

	exchange := func(method, endpoint string, parameters map[string]any) (map[string]any, error) {
		return midServiceExchange(ctx, client, s, options, at, method, endpoint, parameters, false)
	}

	lookup, err := exchange(http.MethodGet, "https://gsa.apple.com/grandslam/GsService2/lookup", nil)
	if err != nil {
		return err
	}
	urls, ok := lookup["urls"].(map[string]any)
	if !ok {
		return errors.New("MidService lookup has no URLs")
	}

	endpoint := func(key string) string { value, _ := urls[key].(string); return value }

	start, err := exchange(http.MethodPost, endpoint("midStartProvisioning"), map[string]any{})
	if err != nil {
		return err
	}

	decode := func(response map[string]any, key string) ([]byte, error) {
		fields, _ := response["Response"].(map[string]any)
		encoded, _ := fields[key].(string)
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(data) == 0 {
			return nil, fmt.Errorf("MidService missing or invalid %s", key)
		}
		return data, nil
	}

	spim, err := decode(start, "spim")
	if err != nil {
		return err
	}
	defer clear(spim)
	pending, err := adi.BeginIdms(s.Uuid, spim, at)
	if err != nil {
		return err
	}
	defer pending.Abort()
	cpim := pending.ClientData()
	defer clear(cpim)
	finish, err := exchange(http.MethodPost, endpoint("midFinishProvisioning"), map[string]any{"cpim": base64.StdEncoding.EncodeToString(cpim)})
	if err != nil {
		return err
	}
	ptm, err := decode(finish, "ptm")
	if err != nil {
		return err
	}
	defer clear(ptm)
	key, err := decode(finish, "tk")
	if err != nil {
		return err
	}
	defer clear(key)
	fields, _ := finish["Response"].(map[string]any)
	routing, err := parseRouting(fields["X-Apple-I-MD-RINFO"])
	if err != nil {
		return err
	}
	record, err := pending.Finish(ptm, key)
	if err != nil {
		return err
	}
	encoded, err := record.MarshalBinary()
	if err != nil {
		return err
	}
	s.Idms, s.Routing, s.IdmsGeneration = encoded, routing, idmsProvisioningGeneration
	s.idms = record
	return nil
}

func parseRouting(value any) (uint64, error) {
	routing, err := strconv.ParseUint(fmt.Sprint(value), 10, 64)
	if err != nil {
		return 0, errors.New("MidService invalid routing info")
	}
	return routing, nil
}

func midServiceExchange(ctx context.Context, client *http.Client, s *installation, options AuthenticatorOptions, at time.Time, method, endpoint string, parameters map[string]any, synchronize bool) (map[string]any, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host != "gsa.apple.com" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid MidService endpoint")
	}
	var body []byte
	if parameters != nil {
		body, err = plist.Marshal(map[string]any{"Header": map[string]any{}, "Request": parameters}, plist.XMLFormat)
		if err != nil {
			return nil, err
		}
		defer clear(body)
	}
	req, err := http.NewRequestWithContext(httpx.WithProfile(ctx, httpx.AuthKit), method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = http.Header{
		"User-Agent": {httpx.DefaultAuthKitUserAgent}, "Accept": {"*/*"}, "Accept-Encoding": {"br, gzip, deflate"}, "Accept-Language": {options.Language}, "Content-Type": {"text/x-xml-plist"},
		"X-Mme-Device-Id": {s.deviceId()}, "X-MMe-Client-Info": {clientInfo(options)}, "X-Apple-I-MD-LU": {s.LocalUser}, "X-Apple-Client-App-Name": {"Setup"}, "X-Apple-I-Client-Time": {at.UTC().Format(time.RFC3339)},
	}
	if synchronize {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-Apple-Client-App-Name", "Apple Configurator")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := httpx.ReadResponseBody(resp, 4_194_304)
	if err != nil {
		return nil, err
	}
	defer clear(raw)
	var result map[string]any
	_, decodeErr := plistutil.Unmarshal(raw, &result)
	if resp.StatusCode != http.StatusOK {
		return nil, &httpx.HttpError{StatusCode: resp.StatusCode}
	}
	if decodeErr != nil {
		return nil, errors.New("invalid MidService response plist")
	}
	if response, ok := result["Response"].(map[string]any); ok {
		status, _ := response["Status"].(map[string]any)
		if ec, ok := status["ec"]; ok && fmt.Sprint(ec) != "0" {
			return nil, fmt.Errorf("MidService rejected request: ec=%v", ec)
		}
	}
	return result, nil
}

func synchronizeIdms(ctx context.Context, client *http.Client, state *installation, options AuthenticatorOptions, status *grandslam.StatusError, at time.Time) error {
	if status == nil || status.Code != -22_421 || status.ServerStatus != 434 || status.DeviceTarget != "native" || len(status.DeviceData) > 128 {
		return errors.New("unsupported IdMS synchronization challenge")
	}
	sim, err := base64.StdEncoding.DecodeString(status.DeviceData)
	if err != nil || len(sim) != 32 {
		return errors.New("invalid IdMS synchronization challenge")
	}
	defer clear(sim)
	record, err := state.idmsRecord()
	if err != nil {
		return err
	}
	mid, srm, err := record.Synchronize(sim, at)
	if err != nil {
		return err
	}
	defer clear(srm)
	lookup, err := midServiceExchange(ctx, client, state, options, at, http.MethodGet, "https://gsa.apple.com/grandslam/GsService2/lookup", nil, true)
	if err != nil {
		return err
	}
	urls, _ := lookup["urls"].(map[string]any)
	endpoint, _ := urls["midSyncMachine"].(string)
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host != "gsa.apple.com" || u.Path != "/grandslam/MidService/syncMachine" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("invalid MidService sync endpoint")
	}
	result, err := midServiceExchange(ctx, client, state, options, at, http.MethodPost, endpoint, map[string]any{
		"srm": base64.StdEncoding.EncodeToString(srm), "X-Apple-I-MD-M": base64.StdEncoding.EncodeToString(mid), "X-Apple-I-MD-RINFO": state.Routing,
	}, true)
	if err != nil {
		return err
	}
	response, _ := result["Response"].(map[string]any)
	inner, _ := response["Status"].(map[string]any)
	if ec, ok := inner["ec"]; !ok || fmt.Sprint(ec) != "0" {
		return errors.New("MidService did not accept synchronization")
	}
	if value, ok := response["X-Apple-I-MD-RINFO"]; ok {
		if state.Routing, err = parseRouting(value); err != nil {
			return err
		}
	}
	return nil
}
