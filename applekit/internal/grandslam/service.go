package grandslam

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"howett.net/plist"

	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
	"github.com/hyperion-cs/appleseum/applekit/internal/plistutil"
)

const (
	serviceUrl                = "https://gsa.apple.com/grandslam/GsService2"
	maxServiceBody            = 1_048_576
	maxExchangeContextLength  = 16_384
	maxNegotiationProofLength = 1_024
)

type Metadata struct {
	ClientInfo string
	Language   string
}

type Service struct {
	Http     *http.Client
	Metadata Metadata
}

type InitRequest struct {
	Username   string
	Public     []byte
	ClientData map[string]any
}

type InitResponse struct {
	Challenge Challenge
	Context   string
	Status    Status
}

type CompleteRequest struct {
	Username   string
	Context    string
	Proof      []byte
	ClientData map[string]any
}

type CompleteResponse struct {
	ServerProof      []byte
	EncryptedSession []byte
	NegotiationProof []byte
	ServerContext    []byte
	Status           Status
}

// Status is independent of HTTP status and may contain secret device data; do not log it verbatim.
type Status struct {
	Code         int64
	ServerStatus int64
	Message      string
	Auth         string
	DeviceData   string
	DeviceTarget string
}

// StatusError preserves structured results while Error omits free-form text and device material.
type StatusError struct {
	HttpStatus int
	Status
}

var (
	ErrCredentialsRejected      = errors.New("Apple rejected the credentials")
	ErrPasswordRejected         = errors.New("Apple rejected the password")
	ErrVerificationCodeRejected = errors.New("Apple rejected the verification code")
)

func (e *StatusError) Is(target error) bool {
	if e.DeviceData != "" || e.ServerStatus == 434 {
		return false
	}
	return target == ErrPasswordRejected && e.Code == -22_406 ||
		target == ErrCredentialsRejected && (e.Code == -22_406 || e.Code == -20_970 || e.Code == -20_101) ||
		target == ErrVerificationCodeRejected && e.Code == -21_669
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("GrandSlam returned result (HTTP %d, ec=%d, hsc=%d)", e.HttpStatus, e.Code, e.ServerStatus)
}

type HttpError = httpx.HttpError

func (s Service) Init(ctx context.Context, input InitRequest) (InitResponse, error) {
	if len(input.Public) == 0 || len(input.Public) > srpGroupSize {
		return InitResponse{}, errors.New("grandslam: invalid client public value")
	}
	response, status, err := s.exchange(ctx, input.Username, input.ClientData, map[string]any{
		"o": "init", "A2k": input.Public, "ps": passwordProtocols(),
	})
	if err != nil {
		return InitResponse{}, err
	}
	iterations, ok := plistutil.Int64(response["i"])
	protocol, _ := response["sp"].(string)
	public, _ := response["B"].([]byte)
	salt, _ := response["s"].([]byte)
	context, _ := response["c"].(string)
	if !ok || iterations < 1 || iterations > maxIterations || (protocol != protocolS2k && protocol != protocolS2kfo) ||
		len(public) == 0 || len(public) > srpGroupSize || len(salt) == 0 || len(salt) > maxSaltLength || len(context) == 0 || len(context) > maxExchangeContextLength {
		return InitResponse{}, errors.New("grandslam: malformed init response")
	}
	return InitResponse{Challenge{salt, public, int(iterations), protocol}, context, status}, nil
}

func (s Service) Complete(ctx context.Context, input CompleteRequest) (CompleteResponse, error) {
	if len(input.Proof) != sha256.Size || len(input.Context) == 0 || len(input.Context) > maxExchangeContextLength {
		return CompleteResponse{}, errors.New("grandslam: invalid client proof or exchange context")
	}
	response, status, err := s.exchange(ctx, input.Username, input.ClientData, map[string]any{
		"o": "complete", "M1": input.Proof, "c": input.Context,
	})
	if err != nil {
		return CompleteResponse{}, err
	}
	proof, _ := response["M2"].([]byte)
	encrypted, _ := response["spd"].([]byte)
	negotiation, validNegotiation := response["np"].([]byte)
	if _, present := response["np"]; present && (!validNegotiation || len(negotiation) == 0) {
		return CompleteResponse{}, errors.New("grandslam: invalid negotiation proof")
	}
	var serverContext []byte
	if value, present := response["sc"]; present {
		var ok bool
		serverContext, ok = value.([]byte)
		if !ok {
			return CompleteResponse{}, errors.New("grandslam: invalid server context")
		}
		if serverContext == nil {
			serverContext = []byte{}
		}
	}
	if len(proof) != sha256.Size || len(encrypted) == 0 || len(encrypted)%aes.BlockSize != 0 || len(negotiation) > maxNegotiationProofLength {
		return CompleteResponse{}, errors.New("grandslam: malformed complete response")
	}
	return CompleteResponse{proof, encrypted, negotiation, serverContext, status}, nil
}

func (s Service) exchange(ctx context.Context, username string, cpd map[string]any, parameters map[string]any) (map[string]any, Status, error) {
	if s.Http == nil || s.Http.Jar == nil || len(username) == 0 || len(username) > maxUsernameLength {
		return nil, Status{}, errors.New("grandslam: shared HTTP client, cookie jar and username required")
	}
	headers, err := s.headers(cpd)
	if err != nil {
		return nil, Status{}, err
	}
	parameters["u"], parameters["cpd"] = username, cpd
	body, err := plist.Marshal(map[string]any{
		"Header": map[string]any{"Version": "1.0.1"}, "Request": parameters,
	}, plist.XMLFormat)
	if err != nil {
		return nil, Status{}, fmt.Errorf("grandslam: encode request: %w", err)
	}
	defer clear(body)
	if len(body) > maxServiceBody {
		return nil, Status{}, errors.New("grandslam: request too large")
	}
	req, err := http.NewRequestWithContext(httpx.WithProfile(ctx, httpx.AuthKit), http.MethodPost, serviceUrl, bytes.NewReader(body))
	if err != nil {
		return nil, Status{}, err
	}
	req.Header = headers
	// Do not forward CPD or device headers through an unexpected redirect.
	resp, err := httpx.NoRedirect(s.Http).Do(req)
	if err != nil {
		return nil, Status{}, fmt.Errorf("grandslam: exchange: %w", err)
	}
	defer resp.Body.Close()
	raw, err := httpx.ReadResponseBody(resp, maxServiceBody)
	if err != nil {
		return nil, Status{}, fmt.Errorf("grandslam: read response: %w", err)
	}
	defer clear(raw)
	response, status, decodeErr := decodeResponse(raw)
	if decodeErr == nil && status.Code != 0 {
		return nil, Status{}, &StatusError{resp.StatusCode, status}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, Status{}, &HttpError{StatusCode: resp.StatusCode}
	}
	return response, status, decodeErr
}

func (s Service) headers(cpd map[string]any) (http.Header, error) {
	if cpd["svct"] != "itunesstore" || cpd["capp"] != "Apple Configurator" || cpd["cbid"] != "com.apple.configurator.ui" {
		return nil, errors.New("grandslam: Configurator service context required")
	}

	valid := func(value string) bool {
		if len(value) == 0 || len(value) > 8_192 || strings.TrimSpace(value) == "" {
			return false
		}
		for _, c := range value {
			if c < ' ' || c > '~' {
				return false
			}
		}
		return true
	}

	if !valid(s.Metadata.ClientInfo) || !valid(s.Metadata.Language) {
		return nil, errors.New("grandslam: invalid client description or language")
	}
	headers := http.Header{}
	for _, key := range []string{"X-Apple-I-MD", "X-Apple-I-MD-M", "X-Apple-I-MD-LU", "X-Apple-I-MD-RINFO", "X-Mme-Device-Id"} {
		value, _ := cpd[key].(string)
		if !valid(value) {
			return nil, fmt.Errorf("grandslam: missing or invalid device field %s", key)
		}
		headers.Set(key, value)
	}
	// AuthKit headers must use the same device snapshot as CPD.
	for key, value := range map[string]string{
		"User-Agent": httpx.AuthKit.UserAgent(), "Accept": "*/*",
		"Accept-Encoding": "gzip, deflate, br", "Accept-Language": s.Metadata.Language,
		"Content-Type": "text/x-xml-plist", "Connection": "keep-alive",
		"X-MMe-Client-Info":       s.Metadata.ClientInfo,
		"X-Apple-AK-Context-Type": "itunesstore", "X-Apple-I-Auth-Type": "2",
		"X-Apple-Client-App-Name":    "Apple Configurator",
		"X-Apple-I-Client-Bundle-Id": "com.apple.configurator.ui",
	} {
		headers.Set(key, value)
	}
	return headers, nil
}

func decodeResponse(raw []byte) (map[string]any, Status, error) {
	if len(raw) == 0 || len(raw) > maxServiceBody {
		return nil, Status{}, errors.New("grandslam: invalid response length")
	}
	var envelope struct{ Response map[string]any }
	if _, err := plistutil.Unmarshal(raw, &envelope); err != nil {
		return nil, Status{}, errors.New("grandslam: invalid response plist")
	}
	wire, _ := envelope.Response["Status"].(map[string]any)
	code, ok := plistutil.Int64(wire["ec"])
	if !ok {
		return nil, Status{}, errors.New("grandslam: missing or invalid server status")
	}
	status := Status{Code: code}
	if value, exists := wire["hsc"]; exists {
		status.ServerStatus, ok = plistutil.Int64(value)
		if !ok || status.ServerStatus < 0 || status.ServerStatus > 999 {
			return nil, Status{}, errors.New("grandslam: invalid inner HTTP status")
		}
	}
	for key, target := range map[string]*string{
		"em": &status.Message, "au": &status.Auth,
		"X-Apple-I-MD-DATA": &status.DeviceData, "X-Apple-I-MD-Cmd-Target": &status.DeviceTarget,
	} {
		if value, present := wire[key]; present {
			*target, ok = value.(string)
			if !ok {
				return nil, Status{}, errors.New("grandslam: invalid server status field")
			}
		}
	}
	return envelope.Response, status, nil
}

// Authenticate verifies SRP; a missing PET means 2FA is still required.
func (s Service) Authenticate(ctx context.Context, username, password string, cpd map[string]any) (SessionData, error) {
	client, err := NewClient(username, password)
	if err != nil {
		return SessionData{}, err
	}
	defer client.Close()
	return s.authenticateWithClient(ctx, username, client, cpd)
}

func (s Service) authenticateWithClient(ctx context.Context, username string, client *Client, cpd map[string]any) (SessionData, error) {
	if client == nil {
		return SessionData{}, fmt.Errorf("grandslam: missing SRP client")
	}
	started, err := s.Init(ctx, InitRequest{Username: username, Public: client.Public(), ClientData: cpd})
	if err != nil {
		return SessionData{}, err
	}
	proof, err := client.Challenge(started.Challenge)
	if err != nil {
		return SessionData{}, err
	}
	completed, err := s.Complete(ctx, CompleteRequest{Username: username, Context: started.Context, Proof: proof, ClientData: cpd})
	if err != nil {
		return SessionData{}, err
	}
	result, err := client.FinishVerified(completed.ServerProof, completed.EncryptedSession, completed.NegotiationProof, completed.ServerContext)
	if err != nil {
		return SessionData{}, err
	}
	return result, nil
}
