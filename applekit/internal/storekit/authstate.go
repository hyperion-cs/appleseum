package storekit

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type SessionRejection struct {
	Operation string
	Reason    string
	Status    int
	Age       time.Duration
	Idle      time.Duration
	AgeKnown  bool
}

func (e *SessionRejection) Error() string {
	if !e.AgeKnown {
		return fmt.Sprintf("%s HTTP %d: Apple refused the operation (session age unknown)", e.Operation+"/"+e.Reason, e.Status)
	}
	return fmt.Sprintf("%s HTTP %d: Apple refused the operation (session age %s, idle %s)", e.Operation+"/"+e.Reason, e.Status, e.Age.Round(time.Second), e.Idle.Round(time.Second))
}

func (e *SessionRejection) Unwrap() error { return ErrSessionExpired }

func authenticationRejected(status int, body []byte) bool {
	if status == http.StatusUnauthorized {
		return true
	}
	if status != http.StatusForbidden {
		return false
	}
	var reply struct {
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if json.Unmarshal(body, &reply) != nil {
		return false
	}
	for _, entry := range reply.Errors {
		if entry.Code == "authentication" {
			return true
		}
	}
	return false
}

func (s *Session) now() time.Time {
	if s.clock != nil {
		return s.clock()
	}
	return time.Now().UTC()
}

func (s *Session) authenticated() { s.metaMu.Lock(); s.lastSuccessAt = s.now(); s.metaMu.Unlock() }

func (s *Session) rejected(operation string, status int) error {
	return s.rejectedFor(operation, status, "authentication")
}

func (s *Session) rejectedFor(operation string, status int, reason string) error {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	e := &SessionRejection{Operation: operation, Status: status, Reason: reason, AgeKnown: !s.authenticatedAt.IsZero()}
	if e.AgeKnown {
		now := s.now()
		e.Age = max(0, now.Sub(s.authenticatedAt))
		if !s.lastSuccessAt.IsZero() {
			e.Idle = max(0, now.Sub(s.lastSuccessAt))
		}
	}
	return e
}

func reauthenticationReason(data map[string]any) string {
	switch numToString(data["failureType"]) {
	case "2034":
		return "token_expired"
	case "2042":
		return "sign_in_required"
	case "1008":
		return "device_verification"
	}
	if message, _ := data["customerMessage"].(string); message == "Your password has changed." {
		return "password_changed"
	}
	return ""
}

var ErrSessionExpired = errors.New("Apple session expired")

func (s *Session) SessionTimes() (time.Time, time.Time) {
	s.metaMu.Lock()
	defer s.metaMu.Unlock()
	return s.authenticatedAt, s.lastSuccessAt
}
