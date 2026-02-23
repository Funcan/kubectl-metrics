package sort

import (
	"sort"
	"testing"
)

func TestNaturalLess(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		// Equal strings.
		{"abc", "abc", false},
		{"", "", false},

		// Empty vs non-empty.
		{"", "a", true},
		{"a", "", false},

		// Pure alphabetic.
		{"abc", "abd", true},
		{"abd", "abc", false},
		{"abc", "abcd", true},

		// Numeric ordering (the main feature).
		{"foo2", "foo10", true},
		{"foo10", "foo2", false},
		{"foo1bar", "foo2bar", true},
		{"foo10bar", "foo9bar", false},

		// Multi-number segments.
		{"a1b2", "a1b10", true},
		{"a2b1", "a10b1", true},

		// Leading zeros.
		{"foo01", "foo1", false}, // numerically equal, but "01" == "1" after strip
		{"foo02", "foo1", false}, // 2 > 1
		{"foo001", "foo01", false},

		// Digit vs non-digit prefix.
		{"1abc", "abc", true}, // '1' < 'a' in ASCII
		{"abc", "1abc", false},

		// Realistic metric names.
		{"http_request_duration_bucket", "http_request_total", true},
		{"metric_1", "metric_2", true},
		{"metric_2", "metric_10", true},
		{"metric_9", "metric_10", true},
		{"metric_10", "metric_20", true},
		{"metric_100", "metric_20", false}, // 100 > 20
	}

	for _, tt := range tests {
		got := NaturalLess(tt.a, tt.b)
		if got != tt.want {
			t.Errorf("NaturalLess(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
		}
	}
}

func TestNaturalSort(t *testing.T) {
	input := []string{
		"metric_20",
		"metric_3",
		"metric_1",
		"metric_100",
		"metric_10",
		"metric_2",
		"alpha",
		"beta",
	}
	want := []string{
		"alpha",
		"beta",
		"metric_1",
		"metric_2",
		"metric_3",
		"metric_10",
		"metric_20",
		"metric_100",
	}

	sort.Slice(input, func(i, j int) bool {
		return NaturalLess(input[i], input[j])
	})

	for i := range want {
		if input[i] != want[i] {
			t.Errorf("index %d: got %q, want %q", i, input[i], want[i])
		}
	}
}
