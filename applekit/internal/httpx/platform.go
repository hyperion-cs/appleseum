package httpx

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

type Platform struct {
	Model   string
	Version string
	Build   string
}

var platformTokens = []*regexp.Regexp{
	regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*[0-9]+,[0-9]+$`),
	regexp.MustCompile(`^[0-9]{1,2}\.[0-9]{1,2}(\.[0-9]{1,2})?$`),
	regexp.MustCompile(`^[0-9]{2}[A-Z][0-9]{1,5}[a-z]?$`),
}

func (p Platform) Validate() error {
	p = p.Defaults()
	for i, value := range []string{p.Model, p.Version, p.Build} {
		if len(value) > 64 || !platformTokens[i].MatchString(value) {
			return errors.New("invalid client platform")
		}
	}
	return nil
}

func (p Platform) Defaults() Platform {
	if p.Model == "" {
		p.Model = DefaultDeviceModel
	}
	if p.Version == "" {
		p.Version = DefaultMacOsVersion
	}
	if p.Build == "" {
		p.Build = DefaultMacOsBuild
	}
	return p
}

func (p Platform) UserAgent(profile Profile) string {
	p = p.Defaults()
	suffix := " (Macintosh; OS X " + p.Version + "; " + p.Build + ") AppleWebKit/1624.2.5.11.4 AMS/1 (dt:1)"
	return strings.Replace(profile.UserAgent(), DefaultMacUserAgentSuffix, suffix, 1)
}

func (p Platform) ClientInfo(application string) string {
	p = p.Defaults()
	return "<" + p.Model + "> <macOS;" + p.Version + ";" + p.Build + "> <" + application + ">"
}

type platformKey struct{}

func WithPlatform(ctx context.Context, platform Platform) context.Context {
	return context.WithValue(ctx, platformKey{}, platform)
}

func PlatformFromContext(ctx context.Context) Platform {
	p, _ := ctx.Value(platformKey{}).(Platform)
	return p.Defaults()
}
