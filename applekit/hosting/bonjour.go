package hosting

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

const (
	bonjourPort        = "5353"
	bonjourTimeout     = time.Second
	bonjourPacketBytes = 16_384
	bonjourReverseName = "1.0.0.127.in-addr.arpa."
)

// BonjourName discovers this device's name using only its local mDNS responder.
func BonjourName(ctx context.Context) (string, error) {
	var failures []error
	for _, host := range []string{"127.0.0.1", "::1"} {
		name, err := lookupBonjour(ctx, net.JoinHostPort(host, bonjourPort))
		if err == nil {
			return name, nil
		}
		failures = append(failures, fmt.Errorf("%s: %w", host, err))
		if ctx.Err() != nil {
			break
		}
	}
	return "", errors.Join(failures...)
}

func lookupBonjour(ctx context.Context, address string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, bonjourTimeout)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(ctx, "udp", address)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	// Closing the connection when the context ends, at the latest after
	// bonjourTimeout, unblocks a read that has no answer coming.
	stop := context.AfterFunc(ctx, func() { connection.Close() })
	defer stop()
	answers, err := queryBonjour(connection, bonjourReverseName, dnsmessage.TypePTR)
	if err != nil {
		return "", err
	}
	for _, answer := range answers {
		ptr, ok := answer.Body.(*dnsmessage.PTRResource)
		if !ok {
			continue
		}
		name, err := NormalizeHost(strings.TrimSuffix(ptr.PTR.String(), "."))
		if err != nil || !strings.HasSuffix(name, ".local") {
			continue
		}
		for _, kind := range []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA} {
			records, err := queryBonjour(connection, name+".", kind)
			if err != nil {
				return "", err
			}
			for _, record := range records {
				var ip net.IP
				switch body := record.Body.(type) {
				case *dnsmessage.AResource:
					ip = body.A[:]
				case *dnsmessage.AAAAResource:
					ip = body.AAAA[:]
				}
				if ip.IsLoopback() {
					return name, nil
				}
			}
		}
	}
	return "", errors.New("no Bonjour name resolving to loopback")
}

func queryBonjour(connection net.Conn, name string, kind dnsmessage.Type) ([]dnsmessage.Resource, error) {
	question, err := dnsmessage.NewName(name)
	if err != nil {
		return nil, err
	}
	request := dnsmessage.Message{Questions: []dnsmessage.Question{{Name: question, Type: kind, Class: dnsmessage.ClassINET}}}
	packet, err := request.Pack()
	if err != nil {
		return nil, err
	}
	if _, err := connection.Write(packet); err != nil {
		return nil, err
	}
	buffer := make([]byte, bonjourPacketBytes)
	for {
		n, err := connection.Read(buffer)
		if err != nil {
			return nil, err
		}
		var response dnsmessage.Message
		if err := response.Unpack(buffer[:n]); err != nil {
			return nil, err
		}
		if !response.Response || response.ID != request.ID {
			continue
		}
		if len(response.Questions) != 1 || !strings.EqualFold(response.Questions[0].Name.String(), name) || response.Questions[0].Type != kind {
			continue
		}
		if response.RCode != dnsmessage.RCodeSuccess || response.Truncated {
			return nil, fmt.Errorf("mDNS response: code=%s truncated=%t", response.RCode, response.Truncated)
		}
		var answers []dnsmessage.Resource
		for _, answer := range response.Answers {
			if answer.Header.Type == kind && strings.EqualFold(answer.Header.Name.String(), name) {
				answers = append(answers, answer)
			}
		}
		return answers, nil
	}
}
