// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2026 Keyoku. All rights reserved.

package main

import (
	"encoding/json"
	"errors"
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
