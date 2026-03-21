// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package llm

import (
	"strings"
	"testing"
)

func TestMemoryType_IsValid(t *testing.T) {
	valid := []MemoryType{
		TypeIdentity, TypePreference, TypeRelationship, TypeEvent,
		TypeActivity, TypePlan, TypeContext, TypeEphemeral,
	}
	for _, mt := range valid {
		if !mt.IsValid() {
			t.Errorf("IsValid(%q) = false, want true", mt)
		}
	}

	invalid := []MemoryType{"", "INVALID", "identity"}
	for _, mt := range invalid {
		if mt.IsValid() {
			t.Errorf("IsValid(%q) = true, want false", mt)
		}
	}
}

func TestMemoryType_StabilityDays(t *testing.T) {
	tests := []struct {
		mt   MemoryType
		want float64
	}{
		{TypeIdentity, 365},
		{TypeEphemeral, 1},
		{TypeContext, 7},
		{"UNKNOWN", 60},
	}
	for _, tt := range tests {
		t.Run(string(tt.mt), func(t *testing.T) {
			if got := tt.mt.StabilityDays(); got != tt.want {
				t.Errorf("StabilityDays(%q) = %v, want %v", tt.mt, got, tt.want)
			}
		})
	}
}

func TestValidateResponse_NilFields(t *testing.T) {
	resp := &ExtractionResponse{}
	if err := validateResponse(resp); err != nil {
		t.Fatalf("validateResponse error = %v", err)
	}
	if resp.Memories == nil {
		t.Error("Memories should be initialized to empty slice")
	}
	if resp.Updates == nil {
		t.Error("Updates should be initialized to empty slice")
	}
	if resp.Deletes == nil {
		t.Error("Deletes should be initialized to empty slice")
	}
	if resp.Resolves == nil {
		t.Error("Resolves should be initialized to empty slice")
	}
	if resp.Skipped == nil {
		t.Error("Skipped should be initialized to empty slice")
	}
}

func TestValidateResponse_ValidMemory(t *testing.T) {
	resp := &ExtractionResponse{
		Memories: []ExtractedMemory{
			{
				Content:    "User likes pizza",
				Type:       "PREFERENCE",
				Importance: 0.7,
				Confidence: 0.9,
			},
		},
	}
	if err := validateResponse(resp); err != nil {
		t.Fatalf("validateResponse error = %v", err)
	}
	if len(resp.Memories) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(resp.Memories))
	}
	if resp.Memories[0].ImportanceFactors == nil {
		t.Error("ImportanceFactors should be initialized to empty slice")
	}
	if resp.Memories[0].ConfidenceFactors == nil {
		t.Error("ConfidenceFactors should be initialized to empty slice")
	}
}

func TestValidateResponse_InvalidType(t *testing.T) {
	resp := &ExtractionResponse{
		Memories: []ExtractedMemory{
			{Content: "test", Type: "INVALID", Importance: 0.5, Confidence: 0.5},
		},
	}
	err := validateResponse(resp)
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
	if !strings.Contains(err.Error(), "invalid type") {
		t.Errorf("error = %v, want 'invalid type'", err)
	}
}

func TestValidateResponse_ImportanceOutOfRange(t *testing.T) {
	tests := []struct {
		name       string
		importance float64
	}{
		{"negative", -0.1},
		{"too high", 1.1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &ExtractionResponse{
				Memories: []ExtractedMemory{
					{Content: "test", Type: "EVENT", Importance: tt.importance, Confidence: 0.5},
				},
			}
			if err := validateResponse(resp); err == nil {
				t.Error("expected error for importance out of range")
			}
		})
	}
}

func TestValidateResponse_ConfidenceOutOfRange(t *testing.T) {
	resp := &ExtractionResponse{
		Memories: []ExtractedMemory{
			{Content: "test", Type: "EVENT", Importance: 0.5, Confidence: 1.5},
		},
	}
	if err := validateResponse(resp); err == nil {
		t.Error("expected error for confidence out of range")
	}
}

func TestValidateResponse_EmptyContent(t *testing.T) {
	resp := &ExtractionResponse{
		Memories: []ExtractedMemory{
			{Content: "", Type: "EVENT", Importance: 0.5, Confidence: 0.5},
		},
	}
	if err := validateResponse(resp); err == nil {
		t.Error("expected error for empty content")
	}
}

func TestValidateResponse_FiltersEmptyUpdates(t *testing.T) {
	resp := &ExtractionResponse{
		Updates: []MemoryUpdate{
			{Query: "find this", NewContent: "new content", Reason: "test"},
			{Query: "", NewContent: "no query", Reason: "test"},
			{Query: "has query", NewContent: "", Reason: "test"},
		},
	}
	if err := validateResponse(resp); err != nil {
		t.Fatalf("validateResponse error = %v", err)
	}
	if len(resp.Updates) != 1 {
		t.Errorf("expected 1 valid update, got %d", len(resp.Updates))
	}
}

func TestValidateResponse_FiltersEmptyDeletes(t *testing.T) {
	resp := &ExtractionResponse{
		Deletes: []MemoryDelete{
			{Query: "find this", Reason: "outdated"},
			{Query: "", Reason: "empty query"},
		},
	}
	if err := validateResponse(resp); err != nil {
		t.Fatalf("validateResponse error = %v", err)
	}
	if len(resp.Deletes) != 1 {
		t.Errorf("expected 1 valid delete, got %d", len(resp.Deletes))
	}
}

func TestValidateResponse_FiltersEmptyResolves(t *testing.T) {
	resp := &ExtractionResponse{
		Resolves: []MemoryResolve{
			{Query: "finish task", Reason: "completed"},
			{Query: "", Reason: "empty query"},
		},
	}
	if err := validateResponse(resp); err != nil {
		t.Fatalf("validateResponse error = %v", err)
	}
	if len(resp.Resolves) != 1 {
		t.Errorf("expected 1 valid resolve, got %d", len(resp.Resolves))
	}
}
