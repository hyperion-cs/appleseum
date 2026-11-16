package api

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"appleseum/backend/internal/fault"
	"appleseum/backend/internal/session"
	"appleseum/backend/internal/wire"
)

const (
	maxJsonBytes = 131_072
)

// statuses gives every code one HTTP status; a code not listed is a conflict
// with the current state.
var statuses = map[fault.Code]int{
	fault.InvalidRequest:        http.StatusBadRequest,
	fault.InvalidSelection:      http.StatusBadRequest,
	fault.SelectionTooLarge:     http.StatusBadRequest,
	fault.InvalidCredentials:    http.StatusBadRequest,
	fault.InvalidIpa:            http.StatusBadRequest,
	fault.InvalidDeviceSettings: http.StatusBadRequest,
	fault.ImportFailed:          http.StatusBadRequest,

	fault.SignInRequired:                http.StatusUnauthorized,
	fault.SessionKeyRequired:            http.StatusUnauthorized,
	fault.SessionExpired:                http.StatusUnauthorized,
	fault.TwoFactorRequired:             http.StatusUnauthorized,
	fault.PasswordRejected:              http.StatusUnauthorized,
	fault.AccountCredentialsRejected:    http.StatusUnauthorized,
	fault.VerificationCodeRejected:      http.StatusUnauthorized,
	fault.VerificationExpired:           http.StatusUnauthorized,
	fault.ReauthenticationFailed:        http.StatusUnauthorized,
	fault.PurchaseHistoryAuthentication: http.StatusUnauthorized,

	fault.ReadOnly:                     http.StatusForbidden,
	fault.DeviceAuthenticationRequired: http.StatusForbidden,
	fault.NotFound:                     http.StatusNotFound,
	fault.ImportTooLarge:               http.StatusRequestEntityTooLarge,
	fault.RateLimited:                  http.StatusTooManyRequests,
	fault.ImportBusy:                   http.StatusTooManyRequests,

	fault.StorageFull:           http.StatusInsufficientStorage,
	fault.QueueFull:             http.StatusTooManyRequests,
	fault.FileTooLarge:          http.StatusRequestEntityTooLarge,
	fault.StorageFailed:         http.StatusInternalServerError,
	fault.LoginProcessingFailed: http.StatusInternalServerError,

	fault.AppleUnavailable:              http.StatusBadGateway,
	fault.AppleResponseUnexpected:       http.StatusBadGateway,
	fault.AppleDeviceVerificationFailed: http.StatusBadGateway,
	fault.AppleSignInDenied:             http.StatusBadGateway,
	fault.DeliveryVerificationFailed:    http.StatusBadGateway,

	fault.ServerUnavailable: http.StatusServiceUnavailable,
}

func statusOf(code fault.Code) int {
	if status, ok := statuses[code]; ok {
		return status
	}
	return http.StatusConflict
}

func writeJson(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// detail attaches a diagnostic to the response in the debug journal.
func detail(w http.ResponseWriter, text string) {
	if response, ok := w.(interface{ SetErrorDetail(string) }); ok {
		response.SetErrorDetail(text)
	}
}

// fail answers with the error's code. An error without a code is a local
// failure: it is reported for diagnostics and the client sees storage_failed.
func (a *Server) fail(w http.ResponseWriter, err error) {
	code := fault.Of(err)
	var carried fault.Code
	if !errors.As(err, &carried) {
		a.report("request", err)
	}
	if a.library != nil {
		a.library.StorageFailure(err)
	}
	var login *session.LoginFailed
	if errors.As(err, &login) {
		detail(w, login.Detail)
	}
	if code == fault.RateLimited {
		w.Header().Set("Retry-After", strconv.Itoa(int(rateWindowDuration/time.Second)))
	}
	status := statusOf(code)
	var refused refusal
	if errors.As(err, &refused) {
		status = refused.status
	}
	detail(w, string(code))
	writeJson(w, status, wire.Error{Code: code})
}

// decode reads one strict JSON value: unknown fields and trailing data are errors.
func decode[T any](w http.ResponseWriter, r *http.Request) (T, error) {
	var value T
	if contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || contentType != "application/json" {
		return value, errMediaType
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxJsonBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil || decoder.Decode(new(any)) != io.EOF {
		return value, fault.InvalidRequest
	}
	return value, nil
}

// handler serves one admitted request; a returned error becomes the response.
type handler func(w http.ResponseWriter, r *http.Request, v visit) error

// A request body is checked before its handler runs, so a handler cannot forget to.
type validated interface{ Validate() error }

// endpoint is a handler with the JSON types it reads and writes, from which
// the frontend's types are generated.
type endpoint struct {
	handle    handler
	request   reflect.Type
	responses []reflect.Type
}

// raw is a handler that writes its own response; the values name its JSON types.
func raw(handle handler, request any, responses ...any) endpoint {
	e := endpoint{handle: handle}
	if request != nil {
		e.request = reflect.TypeOf(request)
	}
	for _, response := range responses {
		e.responses = append(e.responses, reflect.TypeOf(response))
	}
	return e
}

// in decodes and validates a JSON body and answers with the result.
func in[In validated, Out any](status int, op func(r *http.Request, v visit, input In) (Out, error)) endpoint {
	return endpoint{request: reflect.TypeFor[In](), responses: []reflect.Type{reflect.TypeFor[Out]()}, handle: func(w http.ResponseWriter, r *http.Request, v visit) error {
		input, err := decode[In](w, r)
		if err == nil {
			err = input.Validate()
		}
		if err != nil {
			return err
		}
		result, err := op(r, v, input)
		return answer(w, r, status, result, err)
	}}
}

// out answers a request without a body.
func out[Out any](status int, op func(r *http.Request, v visit) (Out, error)) endpoint {
	return endpoint{responses: []reflect.Type{reflect.TypeFor[Out]()}, handle: func(w http.ResponseWriter, r *http.Request, v visit) error {
		result, err := op(r, v)
		return answer(w, r, status, result, err)
	}}
}

// Types lists the JSON types of every route, for generating the frontend's types.
func Types() []reflect.Type {
	a := &Server{cfg: Config{Admin: true}}
	types := []reflect.Type{reflect.TypeFor[wire.Error]()}
	for _, rt := range a.table() {
		if rt.request != nil {
			types = append(types, rt.request)
		}
		types = append(types, rt.responses...)
	}
	return types
}

// none is the result of an operation that answers without content.
type none struct{}

func answer[Out any](w http.ResponseWriter, r *http.Request, status int, result Out, err error) error {
	if err != nil {
		return err
	}
	if _, empty := any(result).(none); empty {
		w.WriteHeader(status)
	} else {
		writeJson(w, status, result)
	}
	return nil
}
