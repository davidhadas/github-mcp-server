// Copyright 2026 IBM. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

/*
Package extendedreverseproxy provides an HTTP reverse proxy that supports detached request/response flows.

# Overview

ExtendedReverseProxy extends the capabilities of net/http/httputil.ReverseProxy to support
scenarios where the upstream response may be delivered to a different downstream request than
the one that initiated the upstream call.

This is useful for sidecar proxies that need to:
  - Forward requests normally (Scenario 1)
  - Return intermediate responses while keeping upstream requests alive, then deliver the
    upstream response to a later resume request (Scenario 2)

# Basic Usage

	proxy := &extendedreverseproxy.ExtendedReverseProxy{
		Classify: func(r *http.Request) (extendedreverseproxy.InboundRequest, error) {
			// Determine if this is a forward request (Type A) or resume request (Type B)
			if key := r.Header.Get("X-Resume-Key"); key != "" {
				return extendedreverseproxy.InboundRequest{
					Kind: extendedreverseproxy.RequestKindResume,
					In:   r,
					Key:  key,
				}, nil
			}
			return extendedreverseproxy.InboundRequest{
				Kind: extendedreverseproxy.RequestKindForward,
				In:   r,
				Key:  generateUniqueKey(),
			}, nil
		},

		Rewrite: func(ur *extendedreverseproxy.UpstreamRequest) {
			// Prepare the upstream request
			ur.Out.URL.Scheme = "http"
			ur.Out.URL.Host = "localhost:8080"
		},

		Decide: func(ur *extendedreverseproxy.UpstreamRequest) (extendedreverseproxy.ProxyAction, error) {
			// Decide how to handle this request
			if needsAuth(ur.In) {
				return extendedreverseproxy.DetachAndWait{
					InterimStatusCode: http.StatusUnauthorized,
					InterimHeader:     http.Header{"X-Resume-Key": []string{ur.Key}},
					InterimBody:       []byte("Authentication required"),
				}, nil
			}
			return extendedreverseproxy.ForwardNow{}, nil
		},

		Transport: http.DefaultTransport,
		Store:     extendedreverseproxy.NewMemoryStore(),
		Timeout:   5 * time.Minute,
	}

	http.Handle("/", proxy)

# Scenarios

Scenario 1 (Normal Forward):
  - Client sends request (Type A)
  - Proxy forwards to upstream
  - Proxy streams upstream response back to client

Scenario 2 (Detach and Resume):
  - Client sends request (Type A)
  - Proxy forwards to upstream
  - Proxy returns interim response (e.g., 401) with correlation key
  - Upstream request continues in background
  - Client sends resume request (Type B) with correlation key
  - Proxy streams upstream response to resume request

# Correlation Keys

The proxy does not generate correlation keys. The caller must provide them via the
Classify function. Keys should be:
  - Unique per request
  - Unguessable (use crypto/rand for security-sensitive applications)
  - Consistent between Type A and Type B requests

# Context Management

For detached flows (Scenario 2), the upstream request uses an independent context
that is not tied to the Type A request's context. This allows the upstream request
to continue after the Type A response is sent.

The upstream context has a timeout specified by the Timeout field (default 5 minutes).

# Error Handling

Errors can occur at multiple points:
  - Classification errors: Return 400 Bad Request
  - Upstream errors: Call ErrorHandler (default: 502 Bad Gateway)
  - Resume key not found: Return 404 Not Found
  - Resume key expired: Cleaned up by background goroutine

# Cleanup

The proxy automatically starts a background goroutine to clean up expired pending
exchanges. Call Close() to stop the cleanup goroutine when shutting down.
*/
package extendedreverseproxy

// Made with Bob
