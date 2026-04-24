// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteInternalErrorWithContext_VectorIndexUnavailable(t *testing.T) {
	rr := httptest.NewRecorder()

	writeInternalErrorWithContext(rr, "search", errors.New("similarity search failed: HNSW search failed: bad index"))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusServiceUnavailable)
	}

	var body apiErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if body.Code != "vector_index_unavailable" {
		t.Fatalf("code = %q, want vector_index_unavailable", body.Code)
	}
	if !body.Retryable {
		t.Fatalf("retryable = false, want true")
	}
}

func TestWriteInternalErrorWithContext_GenericInternal(t *testing.T) {
	rr := httptest.NewRecorder()

	writeInternalErrorWithContext(rr, "remember", errors.New("llm timeout"))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}

	var body apiErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if body.Code != "" {
		t.Fatalf("code = %q, want empty", body.Code)
	}
	if body.Error != "internal server error" {
		t.Fatalf("error = %q, want internal server error", body.Error)
	}
}

func TestWriteInternalErrorWithContext_DeadlineExceededMapsTo504(t *testing.T) {
	rr := httptest.NewRecorder()

	// Simulate the wrapping pattern used by engine_add.go and llm providers.
	wrapped := fmt.Errorf("extraction failed: %w", context.DeadlineExceeded)
	writeInternalErrorWithContext(rr, "remember", wrapped)

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusGatewayTimeout)
	}

	var body apiErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Code != "request_timeout" {
		t.Fatalf("code = %q, want request_timeout", body.Code)
	}
	if !body.Retryable {
		t.Fatalf("retryable = false, want true")
	}
}

func TestWriteInternalErrorWithContext_DeadlineExceededStringFallback(t *testing.T) {
	rr := httptest.NewRecorder()

	// Older provider SDKs may return the stdlib text without wrapping the sentinel.
	writeInternalErrorWithContext(rr, "search", errors.New("provider call failed: context deadline exceeded"))

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusGatewayTimeout)
	}
	var body apiErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body.Code != "request_timeout" {
		t.Fatalf("code = %q, want request_timeout", body.Code)
	}
}

func TestWriteInternalErrorWithContext_CanceledWritesNoResponse(t *testing.T) {
	rr := httptest.NewRecorder()

	wrapped := fmt.Errorf("extraction failed: %w", context.Canceled)
	writeInternalErrorWithContext(rr, "remember", wrapped)

	// Default recorder status is 200; no WriteHeader should have been called.
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want default 200 (no header written), got otherwise", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty — client is gone, we should not write", rr.Body.String())
	}
}

func TestWriteInternalErrorWithContext_SimilarityPrefixWithoutHNSWIs500(t *testing.T) {
	rr := httptest.NewRecorder()

	writeInternalErrorWithContext(rr, "search", errors.New("similarity search failed: database is locked"))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
	}

	var body apiErrorResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if body.Code != "" {
		t.Fatalf("code = %q, want empty", body.Code)
	}
	if body.Retryable {
		t.Fatalf("retryable = true, want false")
	}
}
