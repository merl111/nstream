package server

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"github.com/nspcc-dev/neofs-sdk-go/bearer"
)

// bearerFromRequest extracts an optional NeoFS bearer token from the request.
//
// Recognized sources, in priority order:
//
//  1. "Authorization: Bearer <base64>" header.
//  2. "?bearer=<base64>" query parameter (handy for VLC's "Open Network Stream"
//     where it is awkward to set custom headers).
//
// The base64 payload must be the standard or URL-safe encoding of a marshalled,
// signed bearer.Token. Unknown / missing tokens return (nil, nil): the request
// proceeds unauthenticated, which is fine for public-read containers. Malformed
// tokens return an error so the caller can decide whether to fail closed.
func bearerFromRequest(r *http.Request) (*bearer.Token, error) {
	raw := bearerRaw(r)
	if raw == "" {
		return nil, nil
	}

	data, err := decodeBase64(raw)
	if err != nil {
		return nil, err
	}

	var t bearer.Token
	if err := t.Unmarshal(data); err != nil {
		return nil, err
	}
	return &t, nil
}

func bearerRaw(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		const prefix = "Bearer "
		if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
			return strings.TrimSpace(h[len(prefix):])
		}
	}
	return strings.TrimSpace(r.URL.Query().Get("bearer"))
}

func decodeBase64(s string) ([]byte, error) {
	if s == "" {
		return nil, errors.New("empty token")
	}
	for _, dec := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if data, err := dec.DecodeString(s); err == nil {
			return data, nil
		}
	}
	return nil, errors.New("invalid base64 bearer token")
}
