package storekit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"howett.net/plist"
	"net/http"
	"net/url"
	"strconv"

	"github.com/hyperion-cs/appleseum/applekit/internal/cryptoutil"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
	"github.com/hyperion-cs/appleseum/applekit/internal/plistutil"
)

// Dispatch and asset GETs use the legacy UA; AMD dispatch uses actor metadata.
const (
	DefaultDownloadUserAgent = "Configurator/2.18 (Macintosh; OS X 15.3.2; 24D81) AppleWebKit/0620.2.4.11.6"
)

func (s *Session) resolveDownload(ctx context.Context, appId int64, kb, externalVersionId string, needSinfs bool) (map[string]any, error) {
	if s.amd.Bag.DownloadURL == "" {
		return nil, fmt.Errorf("bag has no volumeStoreDownloadProduct endpoint")
	}
	data, status, err := s.volumeStoreDownload(ctx, appId, kb, externalVersionId, false)
	if err != nil {
		return nil, err
	}
	if item := servedItem(data, needSinfs); item != nil {
		return item, nil
	}
	// Apple's authorization dialog allows one retry with hasBeenAuthedForBuy.
	if dialogIsAuthorization(data) {
		data, status, err = s.volumeStoreDownload(ctx, appId, kb, externalVersionId, true)
		if err != nil {
			return nil, err
		}
		if item := servedItem(data, needSinfs); item != nil {
			return item, nil
		}
	}
	if reason := reauthenticationReason(data); reason != "" {
		return nil, s.rejectedFor("download", status, reason)
	}
	if status == http.StatusUnauthorized {
		return nil, s.rejected("download", status)
	}
	if msg := asString(data["customerMessage"]); msg != "" {
		return nil, fmt.Errorf("download failed: %s", msg)
	}
	return nil, fmt.Errorf("download failed (no songList, HTTP %d, failureType %v)", status, data["failureType"])
}

func (s *Session) volumeStoreDownload(ctx context.Context, appId int64, kb, externalVersionId string, authed bool) (map[string]any, int, error) {
	endpoint, err := url.Parse(s.amd.Bag.DownloadURL)
	if err != nil {
		return nil, 0, err
	}
	query := endpoint.Query()
	query.Set("guid", s.Account.Guid)
	endpoint.RawQuery = query.Encode()
	payload := map[string]any{
		"creditDisplay": "",
		"guid":          s.Account.Guid,
		"kbsync":        kb,
		"salableAdamId": strconv.FormatInt(appId, 10),
		"serialNumber":  fserial(s.Account.Guid),
	}
	if externalVersionId != "" {
		payload["externalVersionId"] = externalVersionId
	}
	if authed {
		payload["hasBeenAuthedForBuy"] = "true"
	}
	body, err := plist.Marshal(payload, plist.XMLFormat)
	if err != nil {
		return nil, 0, err
	}
	for attempt := 0; attempt < 6; attempt++ {
		req, err := http.NewRequestWithContext(httpx.WithProfile(ctx, httpx.Configurator), http.MethodPost, endpoint.String(), bytes.NewReader(body))
		if err != nil {
			return nil, 0, err
		}
		if s.amd.Provider != nil {
			headers, err := s.metadata.Headers()
			if err != nil {
				return nil, 0, err
			}
			req.Header = headers
			if err := s.addAmdHeaders(req.Header); err != nil {
				return nil, 0, err
			}
		} else {
			req.Header.Set("User-Agent", DefaultDownloadUserAgent)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
		req.Header.Set("iCloud-DSID", s.Account.DirectoryServicesId)
		req.Header.Set("X-Dsid", s.Account.DirectoryServicesId)
		req.Header.Set("X-Apple-Store-Front", s.Account.StoreFront)
		req.Header.Set("X-Token", s.Account.PasswordToken)
		resp, err := s.Client.Do(req)
		if err != nil {
			return nil, 0, fmt.Errorf("download request: %w", err)
		}
		raw, readErr := httpx.ReadResponseBody(resp, 8_388_608)
		resp.Body.Close()
		if readErr != nil {
			return nil, resp.StatusCode, readErr
		}
		data := map[string]any{}
		decodeErr := decodePlist(raw, &data)
		action := resp.Header.Get("X-Apple-AMD-Action")
		if action != "" {
			if s.amd.Provider == nil {
				return nil, resp.StatusCode, &DownloadResponseError{Status: resp.StatusCode, AmdAction: action, FailureType: numToString(data["failureType"]), CustomerMessage: asString(data["customerMessage"])}
			}
			if attempt == 5 {
				return nil, resp.StatusCode, &DownloadResponseError{Status: resp.StatusCode, AmdAction: action, Reason: "AMD_action_limit"}
			}
			handled, err := s.amd.HandleAction(ctx, s.Client, s.metadata, resp.Header, s.Account.Guid, s.now())
			if err != nil {
				return nil, resp.StatusCode, fmt.Errorf("download AMD action: %w", err)
			}
			if handled {
				continue
			}
		}
		if authenticationRejected(resp.StatusCode, raw) {
			return nil, resp.StatusCode, s.rejected("download", resp.StatusCode)
		}
		if decodeErr != nil {
			return nil, resp.StatusCode, &DownloadResponseError{Status: resp.StatusCode, Reason: "invalid_response"}
		}
		if resp.StatusCode == http.StatusOK && servedItem(data, false) != nil {
			s.authenticated()
		}
		return data, resp.StatusCode, nil
	}
	return nil, 0, &DownloadResponseError{Reason: "AMD_action_limit"}
}

// DownloadResponseError preserves AMD actions separately from session expiry.
type DownloadResponseError struct {
	Status                                          int
	AmdAction, FailureType, CustomerMessage, Reason string
}

func (e *DownloadResponseError) Error() string {
	return fmt.Sprintf("download HTTP=%d AMD=%q failureType=%q reason=%s message=%q", e.Status, e.AmdAction, e.FailureType, e.Reason, e.CustomerMessage)
}

func servedItem(data map[string]any, needSinfs bool) map[string]any {
	sl, _ := data["songList"].([]any)
	if len(sl) == 0 {
		return nil
	}
	item, _ := sl[0].(map[string]any)
	if item == nil {
		return nil
	}
	if needSinfs {
		if len(extractSinfs(item)) == 0 {
			return nil
		}
	}
	return item
}

func dialogIsAuthorization(data map[string]any) bool {
	d, _ := data["dialog"].(map[string]any)
	if d == nil {
		return false
	}
	kind, _ := d["kind"].(string)
	return kind == "authorization"
}

// fserial encodes the fixed volumeStoreDownload prefix and the GUID's last four bytes.
func fserial(guid string) string {
	b := append([]byte(nil), downloadSerialPrefix[:]...)
	tail := guid
	if len(tail) >= 8 {
		tail = tail[len(tail)-8:]
	}
	if raw, err := hex.DecodeString(tail); err == nil {
		b = append(b, raw...)
	}
	return base64.StdEncoding.EncodeToString(b)
}

func decodePlist(body []byte, value any) error {
	if _, err := plistutil.Unmarshal(body, value); err != nil {
		return fmt.Errorf("decode plist: %w", err)
	}
	return nil
}

var downloadSerialPrefix = cryptoutil.ReadAsset[[5]byte](formatAssets, "assets/download-serial-prefix.bin")
