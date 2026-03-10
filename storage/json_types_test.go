// SPDX-License-Identifier: BSL-1.1
// Copyright (c) 2025 Keyoku. All rights reserved.

package storage

import (
	"testing"
)

func TestStringSlice_Value(t *testing.T) {
	tests := []struct {
		name string
		s    StringSlice
		want string
	}{
		{"nil", nil, "[]"},
		{"empty", StringSlice{}, "[]"},
		{"single", StringSlice{"a"}, `["a"]`},
		{"multiple", StringSlice{"a", "b", "c"}, `["a","b","c"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.s.Value()
			if err != nil {
				t.Fatalf("Value() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Value() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStringSlice_Scan(t *testing.T) {
	t.Run("nil src", func(t *testing.T) {
		var s StringSlice
		if err := s.Scan(nil); err != nil {
			t.Fatalf("Scan(nil) error = %v", err)
		}
		if s != nil {
			t.Errorf("Scan(nil) = %v, want nil", s)
		}
	})

	t.Run("string src", func(t *testing.T) {
		var s StringSlice
		if err := s.Scan(`["a","b"]`); err != nil {
			t.Fatalf("Scan error = %v", err)
		}
		if len(s) != 2 || s[0] != "a" || s[1] != "b" {
			t.Errorf("Scan = %v, want [a b]", s)
		}
	})

	t.Run("byte src", func(t *testing.T) {
		var s StringSlice
		if err := s.Scan([]byte(`["x","y"]`)); err != nil {
			t.Fatalf("Scan error = %v", err)
		}
		if len(s) != 2 || s[0] != "x" || s[1] != "y" {
			t.Errorf("Scan = %v, want [x y]", s)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		var s StringSlice
		if err := s.Scan("not json"); err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("unsupported type", func(t *testing.T) {
		var s StringSlice
		if err := s.Scan(42); err == nil {
			t.Error("expected error for unsupported type")
		}
	})
}

func TestJSONMap_Value(t *testing.T) {
	tests := []struct {
		name string
		m    JSONMap
		want string
	}{
		{"nil", nil, "{}"},
		{"empty", JSONMap{}, "{}"},
		{"simple", JSONMap{"key": "value"}, `{"key":"value"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.m.Value()
			if err != nil {
				t.Fatalf("Value() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("Value() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestJSONMap_Scan(t *testing.T) {
	t.Run("nil src", func(t *testing.T) {
		var m JSONMap
		if err := m.Scan(nil); err != nil {
			t.Fatalf("Scan(nil) error = %v", err)
		}
		if m != nil {
			t.Errorf("Scan(nil) = %v, want nil", m)
		}
	})

	t.Run("string src", func(t *testing.T) {
		var m JSONMap
		if err := m.Scan(`{"key":"val"}`); err != nil {
			t.Fatalf("Scan error = %v", err)
		}
		if m["key"] != "val" {
			t.Errorf("Scan = %v, want {key:val}", m)
		}
	})

	t.Run("byte src", func(t *testing.T) {
		var m JSONMap
		if err := m.Scan([]byte(`{"a":1}`)); err != nil {
			t.Fatalf("Scan error = %v", err)
		}
		if m["a"] != float64(1) {
			t.Errorf("Scan = %v, want {a:1}", m)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		var m JSONMap
		if err := m.Scan("{bad}"); err == nil {
			t.Error("expected error for invalid JSON")
		}
	})

	t.Run("unsupported type", func(t *testing.T) {
		var m JSONMap
		if err := m.Scan(3.14); err == nil {
			t.Error("expected error for unsupported type")
		}
	})

	t.Run("round trip", func(t *testing.T) {
		orig := JSONMap{"nested": map[string]any{"deep": true}, "count": float64(42)}
		val, err := orig.Value()
		if err != nil {
			t.Fatalf("Value() error = %v", err)
		}
		var scanned JSONMap
		if err := scanned.Scan(val); err != nil {
			t.Fatalf("Scan error = %v", err)
		}
		if scanned["count"] != float64(42) {
			t.Errorf("round trip count = %v, want 42", scanned["count"])
		}
	})
}
