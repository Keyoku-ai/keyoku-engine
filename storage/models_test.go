// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package storage

import "testing"

func TestMemoryType_IsValid(t *testing.T) {
	validTypes := []MemoryType{
		TypeIdentity, TypePreference, TypeRelationship, TypeEvent,
		TypeActivity, TypePlan, TypeContext, TypeEphemeral,
	}
	for _, mt := range validTypes {
		if !mt.IsValid() {
			t.Errorf("IsValid(%q) = false, want true", mt)
		}
	}

	invalidTypes := []MemoryType{"", "INVALID", "identity", "unknown"}
	for _, mt := range invalidTypes {
		if mt.IsValid() {
			t.Errorf("IsValid(%q) = true, want false", mt)
		}
	}
}

func TestMemoryType_StabilityDays(t *testing.T) {
	tests := []struct {
		memType MemoryType
		want    float64
	}{
		{TypeIdentity, 365},
		{TypePreference, 270},
		{TypeRelationship, 270},
		{TypeEvent, 120},
		{TypeActivity, 90},
		{TypePlan, 60},
		{TypeContext, 21},
		{TypeEphemeral, 3},
		{"UNKNOWN", 90},
		{"", 90},
	}

	for _, tt := range tests {
		t.Run(string(tt.memType), func(t *testing.T) {
			got := tt.memType.StabilityDays()
			if got != tt.want {
				t.Errorf("StabilityDays(%q) = %v, want %v", tt.memType, got, tt.want)
			}
		})
	}
}
