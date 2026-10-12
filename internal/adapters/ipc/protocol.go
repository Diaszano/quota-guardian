package ipc

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const maxRequest = 1 << 20

type errorResponse struct {
	Error string `json:"error"`
}

func decode(w http.ResponseWriter, r *http.Request, dst any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxRequest+1))
	if err != nil {
		return err
	}
	if len(body) > maxRequest {
		return &http.MaxBytesError{Limit: maxRequest}
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := d.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing request data")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
