// Copyright 2026 IBM. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package extendedreverseproxy

import (
	"net/http"
	"strings"
)

// Hop-by-hop headers. These are removed when sent to the backend.
// As of RFC 7230, hop-by-hop headers are required to appear in the
// Connection header field. These are the headers defined by the
// obsoleted RFC 2616 (section 13.5.1) and are used for backward
// compatibility.
var hopHeaders = []string{
	"Connection",
	"Proxy-Connection", // non-standard but still sent by libcurl and rejected by e.g. google
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Te",      // canonicalized version of "TE"
	"Trailer", // not Trailers per URL above; https://www.rfc-editor.org/errata_search.php?eid=4522
	"Transfer-Encoding",
	"Upgrade",
}

// removeHopByHopHeaders removes hop-by-hop headers from the header map.
func removeHopByHopHeaders(h http.Header) {
	for _, header := range hopHeaders {
		h.Del(header)
	}
}

// removeConnectionHeaders removes headers listed in the Connection header.
// RFC 7230, section 6.1: "The Connection header field allows the sender
// to indicate desired control options for the current connection."
func removeConnectionHeaders(h http.Header) {
	for _, f := range h["Connection"] {
		for _, sf := range strings.Split(f, ",") {
			if sf = strings.TrimSpace(sf); sf != "" {
				h.Del(sf)
			}
		}
	}
}

// copyHeaders copies headers from src to dst, preserving multi-value headers.
func copyHeaders(dst, src http.Header) {
	for k, vv := range src {
		for _, v := range vv {
			dst.Add(k, v)
		}
	}
}

// announceTrailers adds a Trailer header to announce which trailers will be sent.
func announceTrailers(w http.ResponseWriter, resp *http.Response) {
	if len(resp.Trailer) == 0 {
		return
	}

	var trailerKeys []string
	for k := range resp.Trailer {
		trailerKeys = append(trailerKeys, k)
	}
	w.Header().Add("Trailer", strings.Join(trailerKeys, ", "))
}

// headerValuesContainsToken checks if any header value contains the given token.
// Token matching is case-insensitive.
func headerValuesContainsToken(values []string, token string) bool {
	token = strings.ToLower(token)
	for _, v := range values {
		for _, t := range strings.Split(v, ",") {
			if strings.ToLower(strings.TrimSpace(t)) == token {
				return true
			}
		}
	}
	return false
}

// upgradeType returns the connection upgrade type if the request is an upgrade request.
func upgradeType(h http.Header) string {
	if !headerValuesContainsToken(h["Connection"], "Upgrade") {
		return ""
	}
	return h.Get("Upgrade")
}

// Made with Bob
