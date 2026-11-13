package management

import (
	"encoding/base64"
	"errors"
	"io"
)

func readLimit(r io.Reader, n int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, n+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > n {
		return nil, errors.New("management_payload_too_large")
	}
	return b, nil
}
func payloadBytes(e Envelope, n int) ([]byte, error) {
	if len(e.Payload) > base64.RawURLEncoding.EncodedLen(n) {
		return nil, errors.New("management_payload_too_large")
	}
	return base64.RawURLEncoding.DecodeString(e.Payload)
}
