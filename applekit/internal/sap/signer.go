package sap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"

	"github.com/hyperion-cs/appleseum/applekit/internal/sapkey"
)

const (
	ConfiguratorProtocolVersion = uint32(200)
)

var errSignerClosed = errors.New("SAP signer is closed")

type Config struct {
	SetupUrl       string
	CertificateUrl string
	Version        uint32
	HardwareId     []byte
	// Use the same actor metadata as the bag request; the client supplies cookies.
	RequestHeaders http.Header

	// HttpClient must use the shared uTLS transport and the login flow's cookie jar.
	HttpClient *http.Client
}

type ActionSigner interface {
	Sign(input []byte) ([]byte, error)
	Close() error
}

func validateConfig(config Config) error {
	if config.Version != ConfiguratorProtocolVersion {
		return fmt.Errorf("unsupported SAP version %d", config.Version)
	}

	if len(config.HardwareId) == 0 || len(config.HardwareId) > 20 {
		return errors.New("SAP hardware ID must contain between 1 and 20 bytes")
	}

	if err := validateEndpoint("setup", config.SetupUrl); err != nil {
		return err
	}

	if err := validateEndpoint("certificate", config.CertificateUrl); err != nil {
		return err
	}

	return nil
}

func validateEndpoint(label, value string) error {
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil {
		return fmt.Errorf("SAP %s URL must be an absolute HTTPS URL", label)
	}

	return nil
}

type pureSigner struct {
	mu      sync.RWMutex
	closed  bool
	session *sapkey.SigningSession
}

func NewPureSigner(ctx context.Context, config Config) (ActionSigner, error) {
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	if config.HttpClient == nil {
		return nil, errors.New("pure SAP signer requires an HTTP client")
	}
	protocol := setupProtocol{client: config.HttpClient, headers: config.RequestHeaders.Clone()}

	container, err := protocol.certificate(ctx, config.CertificateUrl)
	if err != nil {
		return nil, err
	}
	request, state, err := sapkey.BuildConfiguratorRequest(container)
	if err != nil {
		return nil, fmt.Errorf("build Configurator SAP request: %w", err)
	}
	reply, err := protocol.exchange(ctx, config.SetupUrl, request)
	if err != nil {
		return nil, err
	}
	session, err := sapkey.NewConfiguratorSession(state, reply)
	clear(reply)
	state = sapkey.ConfiguratorRequestState{}
	if err != nil {
		return nil, fmt.Errorf("prepare Configurator SAP session: %w", err)
	}
	return &pureSigner{session: session}, nil
}

func (s *pureSigner) Sign(input []byte) ([]byte, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return nil, errSignerClosed
	}
	nonce, err := sapkey.NewNonce()
	if err != nil {
		return nil, fmt.Errorf("generate SAP action nonce: %w", err)
	}
	return s.session.Sign(nonce, input), nil
}

func (s *pureSigner) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.session.Close()
	s.closed = true
	return nil
}
