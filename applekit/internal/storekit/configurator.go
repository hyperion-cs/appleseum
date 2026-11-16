package storekit

import (
	"errors"
	"net/http"

	"github.com/hyperion-cs/appleseum/applekit/internal/adi"
	"github.com/hyperion-cs/appleseum/applekit/internal/ams"
	"github.com/hyperion-cs/appleseum/applekit/internal/httpx"
)

type savedConfiguratorState struct {
	Amd     []byte `json:"AMD"`
	Bag     ams.Bag
	Context ams.RequestContext
}

func (s *Session) ConfigureAmd(controller *ams.AmdController, metadata ams.RequestContext) error {
	if controller == nil || controller.Provider == nil {
		return errors.New("Configurator download requires AMD provider and bag")
	}
	metadata.Actor = ams.Configurator
	metadata.Storefront = s.Account.StoreFront
	if err := metadata.Validate(); err != nil {
		return err
	}
	download, redownload := s.amd.Bag.DownloadURL, s.amd.Bag.RedownloadURL
	s.amd, s.metadata = *controller, metadata
	s.amd.Bag.DownloadURL, s.amd.Bag.RedownloadURL = download, redownload
	return nil
}

func (s *Session) restoreConfigurator(state *savedConfiguratorState) error {
	if state == nil {
		return nil
	}
	provider, err := adi.ParseAmd(state.Amd)
	if err != nil {
		return err
	}
	return s.ConfigureAmd(&ams.AmdController{Provider: provider, Bag: state.Bag}, state.Context)
}

func (s *Session) Platform() httpx.Platform {
	return s.metadata.Platform.Defaults()
}

// Serialize metadata changes with other session operations.
func (s *Session) SetPlatform(platform httpx.Platform) error {
	if err := platform.Validate(); err != nil {
		return err
	}
	if s.amd.Provider == nil {
		return errors.New("session has no Configurator context")
	}
	s.metadata.Platform = platform.Defaults()
	return nil
}

func (s *Session) SetMetadata(platform httpx.Platform, language string, offset int) error {
	if s.amd.Provider == nil {
		return errors.New("session has no Configurator context")
	}
	metadata := s.metadata
	metadata.Platform, metadata.Language, metadata.UtcOffsetSeconds = platform.Defaults(), language, offset
	if err := metadata.Validate(); err != nil {
		return err
	}
	s.metadata = metadata
	return nil
}

func (s *Session) addAmdHeaders(header http.Header) error {
	if s.amd.Provider == nil {
		return nil
	}
	amd, machine, err := s.amd.Headers(s.now())
	if errors.Is(err, adi.ErrAmdNotProvisioned) {
		return nil
	}
	if err != nil {
		return err
	}
	header.Set("X-Apple-AMD", amd)
	header.Set("X-Apple-AMD-M", machine)
	return nil
}
