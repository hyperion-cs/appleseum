package ams

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"time"

	"howett.net/plist"

	"github.com/hyperion-cs/appleseum/applekit/internal/adi"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
	"github.com/hyperion-cs/appleseum/applekit/internal/plistutil"
)

const (
	maxAmdActionData = 1_048_576
)

var ErrAmdSynchronizeUnavailable = errors.New("AMS AMD provider does not support synchronization")

// AmdProvider must persist installation identity and software record together.
type AmdProvider interface {
	BeginAmd(spim []byte, at time.Time) (adi.AmdProvisioningSession, error)
	HeadersAmd(at time.Time) (adi.AmdHeaders, error)
	EraseAmd() error
}

type AmdSynchronizer interface {
	SynchronizeAmd(sim []byte) (machineId, clientData []byte, err error)
}

// AmdController handles server-directed SP/SM/RP exchanges.
type AmdController struct {
	Bag      Bag
	Provider AmdProvider
}

func (c *AmdController) Headers(at time.Time) (amd, machine string, err error) {
	if c == nil || c.Provider == nil {
		return "", "", errors.New("AMS AMD controller requires a provider")
	}
	values, err := c.Provider.HeadersAmd(at)
	if err != nil {
		return "", "", err
	}
	if len(values.Otp) != 24 || len(values.MachineId) != 60 {
		return "", "", errors.New("AMS AMD provider returned invalid headers")
	}
	return base64.StdEncoding.EncodeToString(values.Otp), base64.StdEncoding.EncodeToString(values.MachineId), nil
}

// HandleAction returns true only after the requested action has been fully applied.
func (c *AmdController) HandleAction(ctx context.Context, client *http.Client, metadata RequestContext, header http.Header, guid string, at time.Time) (bool, error) {
	name := header.Get("X-Apple-AMD-Action")
	if name == "" {
		return false, nil
	}
	if c == nil || c.Provider == nil || client == nil {
		return false, errors.New("AMS AMD action requires controller, bag and client")
	}
	data, err := decodeAmdActionData(header.Get("X-Apple-AMD-Data"))
	if err != nil {
		return false, err
	}
	switch name {
	case "SP":
		if len(data) == 0 || c.Bag.AMDFinishURL == "" {
			return false, errors.New("AMS AMD SP action is incomplete")
		}
		pending, err := c.Provider.BeginAmd(data, at)
		if err != nil {
			return false, err
		}
		if pending == nil {
			return false, errors.New("AMS AMD provider returned no provisioning session")
		}
		defer pending.Abort()
		clientData := pending.ClientData()
		if len(clientData) == 0 || len(clientData) > maxAmdActionData {
			return false, errors.New("AMS AMD provider returned invalid client data")
		}
		var result struct {
			SettingInfo  string `plist:"settingInfo"`
			TransportKey string `plist:"transportKey"`
		}
		if err := postAmd(ctx, client, metadata, c.Bag.AMDFinishURL, map[string]any{
			"clientData": base64.StdEncoding.EncodeToString(clientData),
			"guid":       guid,
		}, &result); err != nil {
			return false, err
		}
		ptm, err := decodeAmdField("settingInfo", result.SettingInfo)
		if err != nil {
			return false, err
		}
		tk, err := decodeAmdField("transportKey", result.TransportKey)
		if err != nil {
			return false, err
		}
		if len(tk) != 16 {
			return false, errors.New("AMS AMD response has invalid transportKey length")
		}
		if err := pending.Finish(ptm, tk); err != nil {
			return false, err
		}
		return true, nil
	case "SM":
		if len(data) == 0 || c.Bag.AMDSyncURL == "" {
			return false, errors.New("AMS AMD SM action is incomplete")
		}
		synchronizer, ok := c.Provider.(AmdSynchronizer)
		if !ok {
			return false, ErrAmdSynchronizeUnavailable
		}
		machineId, clientData, err := synchronizer.SynchronizeAmd(data)
		if err != nil {
			return false, err
		}
		if len(machineId) == 0 || len(clientData) == 0 || len(machineId) > maxAmdActionData || len(clientData) > maxAmdActionData {
			return false, errors.New("AMS AMD synchronizer returned invalid data")
		}
		var result map[string]any
		if err := postAmd(ctx, client, metadata, c.Bag.AMDSyncURL, map[string]any{
			"clientData": base64.StdEncoding.EncodeToString(clientData),
			"machineId":  base64.StdEncoding.EncodeToString(machineId),
		}, &result); err != nil {
			return false, err
		}
		return true, nil
	case "RP":
		if len(data) != 0 {
			return false, errors.New("AMS AMD RP action contains unexpected data")
		}
		if err := c.Provider.EraseAmd(); err != nil {
			return false, err
		}
		return true, nil
	default:
		return false, errors.New("AMS returned an unknown AMD action")
	}
}

func decodeAmdActionData(value string) ([]byte, error) {
	if value == "" {
		return nil, nil
	}
	if len(value) > base64.StdEncoding.EncodedLen(maxAmdActionData) {
		return nil, errors.New("AMS AMD action data exceeds size limit")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) > maxAmdActionData {
		return nil, errors.New("AMS AMD action data is invalid")
	}
	return decoded, nil
}

func decodeAmdField(name, value string) ([]byte, error) {
	decoded, err := decodeAmdActionData(value)
	if err != nil || len(decoded) == 0 {
		return nil, fmt.Errorf("AMS AMD response has invalid %s", name)
	}
	return decoded, nil
}

func postAmd(ctx context.Context, client *http.Client, metadata RequestContext, endpoint string, values map[string]any, output any) error {
	if !httpx.IsAppleUrl(endpoint) {
		return errors.New("invalid AMS AMD endpoint")
	}
	body, err := plist.Marshal(values, plist.XMLFormat)
	if err != nil {
		return fmt.Errorf("encode AMS AMD request: %w", err)
	}
	defer clear(body)
	request, err := http.NewRequestWithContext(httpx.WithProfile(ctx, httpx.NativeAuthentication), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return errors.New("create AMS AMD request")
	}
	headers, err := metadata.Headers()
	if err != nil {
		return err
	}
	request.Header = headers
	request.Header.Set("Content-Type", "application/x-apple-plist")
	response, err := httpx.NoRedirect(client).Do(request)
	if err != nil {
		return fmt.Errorf("AMS AMD exchange: %w", err)
	}
	defer response.Body.Close()
	raw, err := httpx.ReadResponseBody(response, maxNativeResponse)
	if err != nil {
		return fmt.Errorf("read AMS AMD response: %w", err)
	}
	defer clear(raw)
	if response.StatusCode != http.StatusOK {
		return &httpx.HttpError{StatusCode: response.StatusCode}
	}
	if _, err := plistutil.Unmarshal(raw, output); err != nil {
		return errors.New("AMS AMD exchange returned an invalid response")
	}
	return nil
}
