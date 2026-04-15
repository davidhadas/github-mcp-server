// Copyright 2026 IBM. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package extendedreverseproxy

import (
	"context"
	"net/http"
	"sync"
)

// pendingExchange implements PendingExchange.
type pendingExchange struct {
	key        string
	ctx        context.Context
	cancel     context.CancelFunc
	respCh     chan struct{}
	resp       *http.Response
	err        error
	completed  bool
	cancelOnce sync.Once
	mu         sync.Mutex
}

// newPendingExchange creates a new pending exchange with an independent context.
func newPendingExchange(key string, ctx context.Context, cancel context.CancelFunc) *pendingExchange {
	return &pendingExchange{
		key:    key,
		ctx:    ctx,
		cancel: cancel,
		respCh: make(chan struct{}),
	}
}

func (pe *pendingExchange) Key() string {
	return pe.key
}

func (pe *pendingExchange) WaitResponse(ctx context.Context) (*http.Response, error) {
	select {
	case <-pe.respCh:
		pe.mu.Lock()
		defer pe.mu.Unlock()
		return pe.resp, pe.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-pe.ctx.Done():
		pe.mu.Lock()
		defer pe.mu.Unlock()
		if pe.err != nil {
			return nil, pe.err
		}
		return nil, pe.ctx.Err()
	}
}

func (pe *pendingExchange) Cancel(err error) {
	pe.cancelOnce.Do(func() {
		pe.mu.Lock()
		if pe.err == nil {
			pe.err = err
		}
		shouldClose := !pe.completed
		pe.completed = true
		pe.mu.Unlock()

		if shouldClose {
			close(pe.respCh)
		}
		pe.cancel()
	})
}

// setResponse stores the response and signals waiters.
// This should only be called once.
func (pe *pendingExchange) setResponse(resp *http.Response, err error) {
	pe.mu.Lock()
	if pe.completed {
		pe.mu.Unlock()
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		return
	}
	pe.resp = resp
	pe.err = err
	pe.completed = true
	pe.mu.Unlock()
	close(pe.respCh)
}

// Made with Bob
