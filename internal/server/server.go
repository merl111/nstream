// Package server implements an HTTP frontend that streams NeoFS objects to
// HTTP clients (VLC, ffplay, browsers, ...) using RFC 7233 byte ranges, backed
// by NeoFS GetRange requests so payloads are never buffered in full.
package server

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	cid "github.com/nspcc-dev/neofs-sdk-go/container/id"
	oid "github.com/nspcc-dev/neofs-sdk-go/object/id"

	"nstream/internal/neofs"
)

// Server is an http.Handler-producing wrapper around a NeoFS client.
type Server struct {
	nfs *neofs.Client
	log *slog.Logger
}

// New builds a Server backed by the given NeoFS client.
func New(nfs *neofs.Client, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{nfs: nfs, log: log}
}

// Handler returns the root HTTP handler (mux) for the server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	// Net/http's pattern matching trims the prefix; the handler itself parses
	// the remainder so we can support nested filenames containing '/'.
	mux.HandleFunc("/stream/", s.stream)
	mux.HandleFunc("/", s.notFound)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("nstream: NeoFS HTTP video gateway\n" +
			"  GET /stream/{cid}/oid/{oid}\n" +
			"  GET /stream/{cid}/name/{filename...}\n"))
		return
	}
	http.NotFound(w, r)
}

// streamPath represents a parsed /stream URL.
type streamPath struct {
	cid     cid.ID
	oid     oid.ID    // valid only when byOID
	name    string    // valid only when !byOID
	urlPath string    // for MIME sniffing
	byOID   bool
}

// parseStreamPath validates and decomposes a /stream/{cid}/{kind}/{rest} URL.
func parseStreamPath(urlPath string) (streamPath, error) {
	rest := strings.TrimPrefix(urlPath, "/stream/")
	if rest == urlPath || rest == "" {
		return streamPath{}, errors.New("malformed path")
	}

	cidStr, after, ok := strings.Cut(rest, "/")
	if !ok || cidStr == "" {
		return streamPath{}, errors.New("missing container id")
	}
	kind, tail, ok := strings.Cut(after, "/")
	if !ok || tail == "" {
		return streamPath{}, errors.New("missing object selector")
	}

	var sp streamPath
	sp.urlPath = urlPath
	if err := sp.cid.DecodeString(cidStr); err != nil {
		return streamPath{}, errors.New("invalid container id")
	}

	switch kind {
	case "oid":
		// tail must be exactly the object id (no trailing slashes / segments).
		if strings.ContainsRune(tail, '/') {
			return streamPath{}, errors.New("invalid object id")
		}
		if err := sp.oid.DecodeString(tail); err != nil {
			return streamPath{}, errors.New("invalid object id")
		}
		sp.byOID = true
	case "name":
		sp.name = tail
	default:
		return streamPath{}, errors.New("unknown selector: must be 'oid' or 'name'")
	}
	return sp, nil
}
