// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package vectorindex

import (
	"math"
	"testing"
)

const epsilon = 1e-6

func approxEqual(a, b float32) bool {
	return math.Abs(float64(a-b)) < epsilon
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float32
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{"orthogonal", []float32{1, 0, 0}, []float32{0, 1, 0}, 0.0},
		{"opposite", []float32{1, 0, 0}, []float32{-1, 0, 0}, -1.0},
		{"known value", []float32{1, 2, 3}, []float32{4, 5, 6}, float32(32.0 / (math.Sqrt(14) * math.Sqrt(77)))},
		{"single element", []float32{3}, []float32{5}, 1.0},
		{"empty", []float32{}, []float32{}, 0},
		{"mismatched lengths", []float32{1, 2}, []float32{1, 2, 3}, 0},
		{"zero vectors", []float32{0, 0, 0}, []float32{0, 0, 0}, 0},
		{"one zero vector", []float32{1, 2, 3}, []float32{0, 0, 0}, 0},
		{"negative values", []float32{-1, -2, -3}, []float32{-1, -2, -3}, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CosineSimilarity(tt.a, tt.b)
			if !approxEqual(got, tt.want) {
				t.Errorf("CosineSimilarity(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestCosineDistance(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float32
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 0.0},
		{"opposite", []float32{1, 0, 0}, []float32{-1, 0, 0}, 2.0},
		{"orthogonal", []float32{1, 0, 0}, []float32{0, 1, 0}, 1.0},
		{"zero vectors", []float32{0, 0}, []float32{0, 0}, 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CosineDistance(tt.a, tt.b)
			if !approxEqual(got, tt.want) {
				t.Errorf("CosineDistance(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestDotProduct(t *testing.T) {
	tests := []struct {
		name string
		a, b []float32
		want float32
	}{
		{"known value", []float32{1, 2, 3}, []float32{4, 5, 6}, 32},
		{"zero vectors", []float32{0, 0, 0}, []float32{0, 0, 0}, 0},
		{"single element", []float32{3}, []float32{5}, 15},
		{"mismatched lengths", []float32{1, 2}, []float32{1, 2, 3}, 0},
		{"negative values", []float32{-1, 2}, []float32{3, -4}, -11},
		{"empty", []float32{}, []float32{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DotProduct(tt.a, tt.b)
			if !approxEqual(got, tt.want) {
				t.Errorf("DotProduct(%v, %v) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		v    []float32
		want []float32
	}{
		{"3-4-5 triangle", []float32{3, 4}, []float32{0.6, 0.8}},
		{"already unit", []float32{1, 0, 0}, []float32{1, 0, 0}},
		{"single element", []float32{5}, []float32{1}},
		{"zero vector", []float32{0, 0, 0}, []float32{0, 0, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Normalize(tt.v)
			if len(got) != len(tt.want) {
				t.Fatalf("Normalize(%v) length = %d, want %d", tt.v, len(got), len(tt.want))
			}
			for i := range got {
				if !approxEqual(got[i], tt.want[i]) {
					t.Errorf("Normalize(%v)[%d] = %v, want %v", tt.v, i, got[i], tt.want[i])
				}
			}
		})
	}

	// Verify normalized vector has unit length
	t.Run("result is unit length", func(t *testing.T) {
		v := []float32{3, 4, 5}
		normalized := Normalize(v)
		dot := DotProduct(normalized, normalized)
		if !approxEqual(dot, 1.0) {
			t.Errorf("||Normalize(%v)||^2 = %v, want 1.0", v, dot)
		}
	})
}
