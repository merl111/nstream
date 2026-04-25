package server

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	apistatus "github.com/nspcc-dev/neofs-sdk-go/client/status"

	"nstream/internal/neofs"
)

// stream serves HEAD/GET on /stream/{cid}/{oid|name}/{...}.
//
// HEAD returns just the size/type metadata.
// GET without a Range header streams the entire object.
// GET with a Range header streams the precise byte window via ObjectRangeInit.
//
// We always use ObjectGetInit (not ObjectHead) to fetch the object header because
// ObjectHead returns PayloadSize=0 for complex/split objects while ObjectGetInit
// correctly returns the parent object header with the true total payload size.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sp, err := parseStreamPath(r.URL.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	token, err := bearerFromRequest(r)
	if err != nil {
		http.Error(w, "invalid bearer token: "+err.Error(), http.StatusBadRequest)
		return
	}

	objID := sp.oid
	if !sp.byOID {
		id, rerr := s.nfs.ResolveFilename(r.Context(), sp.cid, sp.name, token)
		if rerr != nil {
			s.writeNeoFSError(w, "resolve filename", rerr)
			return
		}
		objID = id
	}

	// GetWithHeader uses ObjectGetInit which returns the *parent* object header
	// for complex objects, giving us the correct total PayloadSize.
	// For non-split objects it behaves identically to ObjectHead + ObjectGet.
	hdr, payload, err := s.nfs.GetWithHeader(r.Context(), sp.cid, objID, token)
	if err != nil {
		s.writeNeoFSError(w, "object get", err)
		return
	}

	size := hdr.PayloadSize()
	contentType := detectContentType(hdr, sp.urlPath)

	s.log.Debug("stream: object opened",
		"cid", sp.cid, "oid", objID,
		"size", size, "type", contentType,
		"range", r.Header.Get("Range"),
	)

	h := w.Header()
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Type", contentType)
	h.Set("ETag", `"`+objID.String()+`"`)
	if name := fileNameAttr(hdr); name != "" {
		h.Set("Content-Disposition", `inline; filename="`+sanitizeQuotedFilename(name)+`"`)
	}

	rangeHeader := r.Header.Get("Range")

	// For HEAD or Range requests we don't need the open payload stream right now.
	if r.Method == http.MethodHead || rangeHeader != "" {
		payload.Close()
		payload = nil
	}

	if r.Method == http.MethodHead {
		h.Set("Content-Length", strconv.FormatUint(size, 10))
		w.WriteHeader(http.StatusOK)
		return
	}

	// --- Full object (no Range header) ---
	if rangeHeader == "" {
		h.Set("Content-Length", strconv.FormatUint(size, 10))
		if _, err := io.Copy(w, payload); err != nil {
			s.log.Debug("stream copy aborted", "err", err, "cid", sp.cid, "oid", objID)
		}
		payload.Close()
		return
	}

	// --- Byte-range request ---
	offset, length, status, err := parseRange(rangeHeader, size)
	if err != nil {
		if errors.Is(err, errMultiRange) {
			h.Set("Content-Range", fmt.Sprintf("bytes */%d", size))
			http.Error(w, "multi-range not supported", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		http.Error(w, "invalid range: "+err.Error(), http.StatusBadRequest)
		return
	}

	switch status {
	case rangeUnsat:
		h.Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		http.Error(w, "range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
		return

	case rangeNone:
		// parseRange only returns rangeNone when rangeHeader == "", which
		// we've already handled above. Shouldn't reach here.
		h.Set("Content-Length", strconv.FormatUint(size, 10))
		reader, rerr := s.nfs.Get(r.Context(), sp.cid, objID, token)
		if rerr != nil {
			s.writeNeoFSError(w, "object get (fallback)", rerr)
			return
		}
		defer reader.Close()
		if _, err := io.Copy(w, reader); err != nil {
			s.log.Debug("stream copy aborted", "err", err, "cid", sp.cid, "oid", objID)
		}
		return

	case rangeOK:
		end := offset + length - 1
		// "bytes=0-" (or any full-length range) should not force us to stream the
		// whole object in a single NeoFS read. Some complex/split objects fail
		// mid-stream in that mode. Serve an initial bounded chunk and let the
		// client request subsequent ranges.
		if offset == 0 && length == size {
			const firstChunk uint64 = 8 * 1024 * 1024 // 8 MiB
			if size > firstChunk {
				length = firstChunk
				end = offset + length - 1
			}
		}
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, end, size))
		h.Set("Content-Length", strconv.FormatUint(length, 10))
		w.WriteHeader(http.StatusPartialContent)

		reader, rerr := s.nfs.Range(r.Context(), sp.cid, objID, offset, length, token)
		if rerr != nil {
			s.log.Error("open NeoFS range stream",
				"err", rerr, "cid", sp.cid, "oid", objID, "off", offset, "len", length)
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, herr := hj.Hijack(); herr == nil {
					_ = conn.Close()
				}
			}
			return
		}
		defer reader.Close()
		if _, err := io.Copy(w, reader); err != nil {
			s.log.Debug("stream copy aborted", "err", err, "cid", sp.cid, "oid", objID)
		}
	}
}

// writeNeoFSError translates SDK errors into HTTP responses. Headers must not
// have been written yet.
func (s *Server) writeNeoFSError(w http.ResponseWriter, op string, err error) {
	switch {
	case errors.Is(err, neofs.ErrFilenameNotFound),
		errors.Is(err, apistatus.ErrObjectNotFound),
		errors.Is(err, apistatus.ErrContainerNotFound),
		errors.Is(err, apistatus.ErrObjectAlreadyRemoved):
		http.Error(w, op+": "+err.Error(), http.StatusNotFound)
	case errors.Is(err, apistatus.ErrObjectAccessDenied):
		http.Error(w, op+": access denied", http.StatusForbidden)
	case errors.Is(err, apistatus.ErrObjectOutOfRange):
		http.Error(w, op+": range not satisfiable", http.StatusRequestedRangeNotSatisfiable)
	default:
		s.log.Error(op, "err", err)
		http.Error(w, op+": "+err.Error(), http.StatusBadGateway)
	}
}

// sanitizeQuotedFilename strips characters that would break a quoted-string
// HTTP header value.
func sanitizeQuotedFilename(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' || c == '\\' || c == '\r' || c == '\n' {
			continue
		}
		out = append(out, c)
	}
	return string(out)
}
