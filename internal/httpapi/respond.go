package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"solo-0014-zone-rollout-control/internal/domain"
)

const requestLimit = 1 << 20

type successBody struct {
	Data any          `json:"data"`
	Meta responseMeta `json:"meta"`
}

type responseMeta struct {
	RequestID string `json:"request_id,omitempty"`
}

type failureBody struct {
	Error domain.DomainError `json:"error"`
}

// strictContract marks a decoded request payload: fields returned by
// RequiredRequestFields must appear in the JSON object. Unknown fields are
// always rejected by the decoder itself.
type strictContract interface {
	RequiredRequestFields() []string
}

// decode reads a required JSON object body, rejects unknown or missing fields,
// and never lets an unrecognized or incomplete payload reach a service.
func decode(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, requestLimit)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		return domain.Validation("request body must contain valid JSON", map[string]any{
			"reason": err.Error(),
		})
	}
	if len(bytes.TrimSpace(payload)) == 0 {
		return domain.Validation("request body must contain a JSON object", nil)
	}
	if err := decodePayload(payload, target); err != nil {
		return domain.Validation("request body must contain valid JSON", map[string]any{
			"reason": err.Error(),
		})
	}
	if contract, ok := target.(strictContract); ok {
		if err := validateRequiredFields(payload, contract.RequiredRequestFields()); err != nil {
			return err
		}
	}
	return nil
}

// decodeEmpty is used by endpoints that carry no request body. It accepts an
// empty body and rejects any payload that was sent.
func decodeEmpty(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, requestLimit)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		return domain.Validation("request body must be empty", map[string]any{
			"reason": err.Error(),
		})
	}
	if len(bytes.TrimSpace(payload)) != 0 {
		return domain.Validation("request body must be empty", nil)
	}
	return nil
}

func decodePayload(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func validateRequiredFields(payload []byte, required []string) error {
	object := make(map[string]json.RawMessage)
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return domain.Validation("request body must contain a JSON object", nil)
	}
	missing := make([]any, 0)
	for _, field := range required {
		raw, ok := object[field]
		if !ok || string(bytes.TrimSpace(raw)) == "null" {
			missing = append(missing, field)
		}
	}
	if len(missing) > 0 {
		return domain.Validation("request body is incomplete", map[string]any{
			"missing_fields": missing,
		})
	}
	return nil
}

func sendData(w http.ResponseWriter, r *http.Request, status int, value any) {
	sendJSON(w, status, successBody{
		Data: value,
		Meta: responseMeta{RequestID: requestID(r.Context())},
	})
}

func sendError(w http.ResponseWriter, r *http.Request, err error) {
	var target *domain.DomainError
	if !errors.As(err, &target) {
		target = domain.NewError(domain.CodeInternal, "internal server error", nil)
	}
	sendJSON(w, statusCode(target.Code), failureBody{Error: *target})
}

func sendJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func statusCode(code domain.ErrorCode) int {
	switch code {
	case domain.CodeValidationFailed:
		return http.StatusBadRequest
	case domain.CodeNotFound:
		return http.StatusNotFound
	case domain.CodeInvalidState,
		domain.CodeVersionConflict,
		domain.CodeIdempotencyConflict,
		domain.CodeDuplicateRevision,
		domain.CodeNodeUnavailable,
		domain.CodeLeaseExpired,
		domain.CodeLeaseNotClaimed,
		domain.CodeConflict:
		return http.StatusConflict
	case domain.CodeNoProbes:
		return http.StatusUnprocessableEntity
	case domain.CodeStorageFailure, domain.CodeInternal:
		return http.StatusInternalServerError
	default:
		return http.StatusInternalServerError
	}
}
