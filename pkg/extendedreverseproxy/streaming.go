// Copyright 2026 IBM. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package extendedreverseproxy

import (
	"io"
	"net/http"
	"time"
)

// streamResponse streams an HTTP response to a ResponseWriter without buffering the full body.
func streamResponse(w http.ResponseWriter, resp *http.Response, flushInterval time.Duration, bufferPool BufferPool) error {
	defer resp.Body.Close()

	// Copy response headers
	copyHeaders(w.Header(), resp.Header)
	removeConnectionHeaders(w.Header())
	removeHopByHopHeaders(w.Header())

	// Announce trailers
	announceTrailers(w, resp)

	// Write status code
	w.WriteHeader(resp.StatusCode)

	// Stream body with flushing
	if err := copyWithFlush(w, resp.Body, flushInterval, bufferPool); err != nil {
		return err
	}

	// Copy trailers
	copyHeaders(w.Header(), resp.Trailer)

	return nil
}

// copyWithFlush copies from src to dst with periodic flushing for streaming responses.
func copyWithFlush(dst io.Writer, src io.Reader, flushInterval time.Duration, bufferPool BufferPool) error {
	if flushInterval <= 0 {
		_, err := io.Copy(dst, src)
		return err
	}

	flusher, ok := dst.(http.Flusher)
	if !ok {
		// If dst doesn't support flushing, fall back to regular copy
		_, err := io.Copy(dst, src)
		return err
	}

	buf := bufferPool.Get()
	defer bufferPool.Put(buf)

	lastFlush := time.Now()

	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, writeErr := dst.Write(buf[:n]); writeErr != nil {
				return writeErr
			}

			if time.Since(lastFlush) > flushInterval {
				flusher.Flush()
				lastFlush = time.Now()
			}
		}

		if err == io.EOF {
			flusher.Flush()
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// Made with Bob
