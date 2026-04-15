// Copyright 2026 IBM. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package extendedreverseproxy

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
)

// ExtendedReverseProxy is an HTTP reverse proxy that supports detached request/response flows.
// It extends the capabilities of httputil.ReverseProxy to allow:
//   - Normal forwarding (Scenario 1)
//   - Detached flows where an interim response is sent while the upstream request continues,
//     and the upstream response is later delivered to a resume request (Scenario 2)
type ExtendedReverseProxy struct {
	// Classify determines the kind of inbound request and provides a correlation key.
	// Required.
	Classify ClassifyFunc

	// Rewrite prepares the upstream request.
	// Required.
	Rewrite RewriteFunc

	// Decide determines how to handle the request after upstream preparation.
	// Required.
	Decide DecideFunc

	// ModifyResponse allows modification of the upstream response before streaming.
	// Optional.
	ModifyResponse ModifyResponseFunc

	// ErrorHandler handles errors during proxy operation.
	// If nil, a default handler is used that logs the error and returns 502 Bad Gateway.
	ErrorHandler ErrorHandlerFunc

	// Transport specifies the mechanism by which individual HTTP requests are made.
	// If nil, http.DefaultTransport is used.
	Transport http.RoundTripper

	// FlushInterval specifies the flush interval for streaming responses.
	// If zero, no periodic flushing is done.
	// A negative value means to flush immediately after each write.
	FlushInterval time.Duration

	// BufferPool optionally specifies a buffer pool to use for copying response bodies.
	// If nil, a default pool is used.
	BufferPool BufferPool

	// ErrorLog specifies an optional logger for errors.
	// If nil, logging is done via the log package's standard logger.
	ErrorLog *log.Logger

	// Store manages pending exchanges for detached flows.
	// Required for DetachAndWait actions.
	Store PendingStore

	// Timeout specifies the maximum duration for pending exchanges.
	// If zero, a default of 5 minutes is used.
	Timeout time.Duration

	// Internal state
	cleanupOnce sync.Once
	cleanupDone chan struct{}
}

// ServeHTTP implements http.Handler.
func (p *ExtendedReverseProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.startCleanup()

	// Classify request
	inbound, err := p.Classify(r)
	if err != nil {
		p.handleError(w, r, fmt.Errorf("classification failed: %w", err))
		return
	}

	switch inbound.Kind {
	case RequestKindForward:
		p.handleForward(w, inbound)
	case RequestKindResume:
		p.handleResume(w, inbound)
	default:
		p.handleError(w, r, fmt.Errorf("unknown request kind: %d", inbound.Kind))
	}
}

// handleForward handles Type A requests (normal forward or detach).
func (p *ExtendedReverseProxy) handleForward(w http.ResponseWriter, inbound InboundRequest) {
	// Use original request directly - don't clone yet to preserve body
	// Cloning will happen later in dispatchUpstream() if DetachAndWait is chosen
	upstream := &UpstreamRequest{
		In:  inbound.In,
		Out: inbound.In, // Use original request
		Key: inbound.Key,
	}

	// Rewrite
	if p.Rewrite != nil {
		p.Rewrite(upstream)
	}

	// Decide action
	action, err := p.Decide(upstream)
	if err != nil {
		p.handleError(w, inbound.In, err)
		return
	}

	switch act := action.(type) {
	case ForwardNow:
		p.doForwardNow(w, inbound.In, upstream)
	case RespondNow:
		p.doRespondNow(w, act)
	case DetachAndWait:
		p.doDetachAndWait(w, inbound.In, upstream, act)
	case ForwardWithDetachOption:
		p.doForwardWithDetachOption(w, inbound.In, upstream, act)
	default:
		p.handleError(w, inbound.In, fmt.Errorf("unknown action type"))
	}
}

// doForwardNow performs normal forwarding (Scenario 1).
func (p *ExtendedReverseProxy) doForwardNow(w http.ResponseWriter, r *http.Request, upstream *UpstreamRequest) {
	// Remove hop-by-hop headers from request
	removeConnectionHeaders(upstream.Out.Header)
	removeHopByHopHeaders(upstream.Out.Header)

	resp, err := p.transport().RoundTrip(upstream.Out)
	if err != nil {
		p.handleError(w, r, err)
		return
	}

	if p.ModifyResponse != nil {
		if err := p.ModifyResponse(resp); err != nil {
			resp.Body.Close()
			p.handleError(w, r, err)
			return
		}
	}

	if err := streamResponse(w, resp, p.FlushInterval, p.bufferPool()); err != nil {
		p.logf("error streaming response: %v", err)
	}
}

// doRespondNow sends a local response without waiting for upstream.
func (p *ExtendedReverseProxy) doRespondNow(w http.ResponseWriter, action RespondNow) {
	if action.Header != nil {
		copyHeaders(w.Header(), action.Header)
	}
	w.WriteHeader(action.StatusCode)
	if len(action.Body) > 0 {
		w.Write(action.Body)
	}
}

// doDetachAndWait starts a detached upstream request and sends an interim response (Scenario 2).
func (p *ExtendedReverseProxy) doDetachAndWait(w http.ResponseWriter, r *http.Request, upstream *UpstreamRequest, action DetachAndWait) {
	timeout := p.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	pe := newPendingExchange(upstream.Key, ctx, cancel)

	expiresAt := time.Now().Add(timeout)
	if err := p.Store.Put(upstream.Key, pe, expiresAt); err != nil {
		pe.Cancel(err)
		p.handleError(w, r, fmt.Errorf("failed to store pending exchange: %w", err))
		return
	}

	go p.dispatchUpstream(pe, upstream.Out)

	if action.InterimHeader != nil {
		copyHeaders(w.Header(), action.InterimHeader)
	}
	w.WriteHeader(action.InterimStatusCode)
	if len(action.InterimBody) > 0 {
		w.Write(action.InterimBody)
	}
}

// doForwardWithDetachOption forwards normally but can be interrupted to detach mid-flight.
func (p *ExtendedReverseProxy) doForwardWithDetachOption(w http.ResponseWriter, r *http.Request, upstream *UpstreamRequest, action ForwardWithDetachOption) {
	// Create an independent context for the upstream request that won't be cancelled
	// when the initial client request completes (in case we detach)
	timeout := p.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	upstreamCtx, upstreamCancel := context.WithTimeout(context.Background(), timeout)

	// Clone request with independent context
	upstreamReq := upstream.Out.Clone(upstreamCtx)
	upstreamReq.RequestURI = ""

	// Remove hop-by-hop headers from request
	removeConnectionHeaders(upstreamReq.Header)
	removeHopByHopHeaders(upstreamReq.Header)

	// Channel to receive upstream response
	type result struct {
		resp *http.Response
		err  error
	}
	resultChan := make(chan result, 1)

	// Start upstream request in goroutine
	go func() {
		resp, err := p.transport().RoundTrip(upstreamReq)
		resultChan <- result{resp: resp, err: err}
	}()

	// Wait for either upstream response or detach signal
	select {
	case detachAction := <-action.DetachSignal:
		// OAuth discovered! Detach without cancelling the upstream request.
		// The upstream request continues in the background and will be stored
		// for the resume request to retrieve later.

		// Create pending exchange using the same context as the upstream request
		pe := newPendingExchange(upstream.Key, upstreamCtx, upstreamCancel)

		expiresAt := time.Now().Add(timeout)
		if err := p.Store.Put(upstream.Key, pe, expiresAt); err != nil {
			pe.Cancel(err)
			upstreamCancel()
			// Drain the result channel to avoid goroutine leak
			go func() {
				res := <-resultChan
				if res.resp != nil {
					res.resp.Body.Close()
				}
			}()
			p.handleError(w, r, fmt.Errorf("failed to store pending exchange: %w", err))
			return
		}

		// Transfer the ongoing upstream request to the pending exchange
		go func() {
			res := <-resultChan
			if res.err != nil {
				pe.Cancel(res.err)
				return
			}
			pe.setResponse(res.resp, nil)
		}()

		// Send interim response to client
		if detachAction.InterimHeader != nil {
			copyHeaders(w.Header(), detachAction.InterimHeader)
		}
		w.WriteHeader(detachAction.InterimStatusCode)
		if len(detachAction.InterimBody) > 0 {
			w.Write(detachAction.InterimBody)
		}

	case res := <-resultChan:
		// Normal response received before detach signal - clean up the upstream context
		defer upstreamCancel()
		// Normal response received before detach signal
		if res.err != nil {
			p.handleError(w, r, res.err)
			return
		}

		if p.ModifyResponse != nil {
			if err := p.ModifyResponse(res.resp); err != nil {
				res.resp.Body.Close()
				p.handleError(w, r, err)
				return
			}
		}

		if err := streamResponse(w, res.resp, p.FlushInterval, p.bufferPool()); err != nil {
			p.logf("error streaming response: %v", err)
		}
	}
}

// dispatchUpstream performs the upstream request and stores the result in the pending exchange.
func (p *ExtendedReverseProxy) dispatchUpstream(pe *pendingExchange, outReq *http.Request) {
	outReq = outReq.Clone(pe.ctx)
	outReq.RequestURI = ""

	// Remove hop-by-hop headers from request
	removeConnectionHeaders(outReq.Header)
	removeHopByHopHeaders(outReq.Header)

	resp, err := p.transport().RoundTrip(outReq)
	if err != nil {
		pe.Cancel(err)
		return
	}
	pe.setResponse(resp, nil)
}

// handleResume handles Type B requests (resume).
func (p *ExtendedReverseProxy) handleResume(w http.ResponseWriter, inbound InboundRequest) {
	// Lookup pending exchange
	pe, ok := p.Store.Get(inbound.Key)
	if !ok {
		http.Error(w, "pending exchange not found", http.StatusNotFound)
		return
	}

	// Clean up after we're done
	defer p.Store.Delete(inbound.Key)
	defer pe.Cancel(nil) // Cancel the pending exchange context after we're done

	// Wait for response
	resp, err := pe.WaitResponse(inbound.In.Context())
	if err != nil {
		_ = p.Store.Delete(inbound.Key)
		pe.Cancel(err)
		p.handleError(w, inbound.In, fmt.Errorf("upstream failed: %w", err))
		return
	}

	if p.ModifyResponse != nil {
		if err := p.ModifyResponse(resp); err != nil {
			resp.Body.Close()
			p.handleError(w, inbound.In, err)
			return
		}
	}

	// Stream response
	if err := streamResponse(w, resp, p.FlushInterval, p.bufferPool()); err != nil {
		p.logf("error streaming response: %v", err)
	}
}

// transport returns the RoundTripper to use for upstream requests.
func (p *ExtendedReverseProxy) transport() http.RoundTripper {
	if p.Transport != nil {
		return p.Transport
	}
	return http.DefaultTransport
}

// bufferPool returns the BufferPool to use for copying response bodies.
func (p *ExtendedReverseProxy) bufferPool() BufferPool {
	if p.BufferPool != nil {
		return p.BufferPool
	}
	return defaultBufferPool
}

// handleError handles errors during proxy operation.
func (p *ExtendedReverseProxy) handleError(w http.ResponseWriter, r *http.Request, err error) {
	if p.ErrorHandler != nil {
		p.ErrorHandler(w, r, err)
		return
	}

	p.logf("proxy error: %v", err)
	http.Error(w, "Bad Gateway", http.StatusBadGateway)
}

// logf logs a message using the configured logger.
func (p *ExtendedReverseProxy) logf(format string, args ...interface{}) {
	if p.ErrorLog != nil {
		p.ErrorLog.Printf(format, args...)
	} else {
		log.Printf(format, args...)
	}
}

// startCleanup starts the background cleanup goroutine if not already started.
func (p *ExtendedReverseProxy) startCleanup() {
	p.cleanupOnce.Do(func() {
		if p.Store == nil {
			return
		}
		p.cleanupDone = make(chan struct{})
		go p.cleanupLoop()
	})
}

// cleanupLoop periodically cleans up expired pending exchanges.
func (p *ExtendedReverseProxy) cleanupLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			count := p.Store.CleanupExpired()
			if count > 0 {
				p.logf("cleaned up %d expired pending exchanges", count)
			}
		case <-p.cleanupDone:
			return
		}
	}
}

// Close stops the background cleanup goroutine.
func (p *ExtendedReverseProxy) Close() error {
	if p.cleanupDone != nil {
		close(p.cleanupDone)
	}
	return nil
}

// defaultBufferPool is the default buffer pool used for copying response bodies.
var defaultBufferPool = &syncBufferPool{}

type syncBufferPool struct {
	pool sync.Pool
}

func (p *syncBufferPool) Get() []byte {
	if v := p.pool.Get(); v != nil {
		return v.([]byte)
	}
	return make([]byte, 32*1024)
}

func (p *syncBufferPool) Put(b []byte) {
	p.pool.Put(b)
}

// Made with Bob
