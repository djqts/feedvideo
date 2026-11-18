package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

var errVerificationRequest = errors.New("invalid verification request")

// A map/*bool decode cannot detect duplicate or case-aliased intent. Read a
// bounded body, then require one complete object and one exact boolean field.
func parseVerificationRequest(w http.ResponseWriter, req *http.Request) (bool, error) {
	if req.Body == nil {
		return false, errVerificationRequest
	}
	reader := http.MaxBytesReader(w, req.Body, 8*1024)
	defer reader.Close()
	raw, err := io.ReadAll(reader)
	if err != nil {
		return false, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false, errVerificationRequest
	}
	seen, value := false, false
	for decoder.More() {
		token, err = decoder.Token()
		if err != nil {
			return false, errVerificationRequest
		}
		field, ok := token.(string)
		if !ok {
			return false, errVerificationRequest
		}
		var fieldValue json.RawMessage
		if err := decoder.Decode(&fieldValue); err != nil {
			return false, errVerificationRequest
		}
		if strings.EqualFold(field, "verified") {
			if field != "verified" || seen {
				return false, errVerificationRequest
			}
			seen = true
			switch string(bytes.TrimSpace(fieldValue)) {
			case "true":
				value = true
			case "false":
				value = false
			default:
				return false, errVerificationRequest
			}
		}
	}
	token, err = decoder.Token()
	if err != nil || token != json.Delim('}') || !seen {
		return false, errVerificationRequest
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return false, errVerificationRequest
	}
	return value, nil
}
