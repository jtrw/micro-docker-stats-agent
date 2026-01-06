package main

import "testing"

func TestParsePercent(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want float64
	}{
		{"empty", "", 0},
		{"percent", "12.5%", 12.5},
		{"spaces", " 3.25 % ", 3.25},
		{"invalid", "not-a-number", 0},
	}

	for _, tt := range tests {
		if got := parsePercent(tt.in); got != tt.want {
			t.Fatalf("%s: parsePercent(%q)=%v want %v", tt.name, tt.in, got, tt.want)
		}
	}
}

func TestParseSizeBytes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want uint64
	}{
		{"empty", "", 0},
		{"bytes", "512B", 512},
		{"kilobytes", "1.5kB", 1500},
		{"megabytes", "2MB", 2_000_000},
		{"gibibytes", "1GiB", 1 << 30},
		{"round", "0.4MB", 400_000},
		{"invalid", "oops", 0},
	}

	for _, tt := range tests {
		if got := parseSizeBytes(tt.in); got != tt.want {
			t.Fatalf("%s: parseSizeBytes(%q)=%d want %d", tt.name, tt.in, got, tt.want)
		}
	}
}

func TestParseMemUsage(t *testing.T) {
	usage, limit := parseMemUsage("12.5MiB / 2GiB")
	if usage != 13_107_200 {
		t.Fatalf("unexpected usage: %d", usage)
	}
	if limit != 2_147_483_648 {
		t.Fatalf("unexpected limit: %d", limit)
	}
}

func TestParseTwoSizes(t *testing.T) {
	left, right := parseTwoSizes("1kB / 2MB")
	if left != 1_000 {
		t.Fatalf("left=%d want %d", left, 1_000)
	}
	if right != 2_000_000 {
		t.Fatalf("right=%d want %d", right, 2_000_000)
	}
}

func TestSplitTwo(t *testing.T) {
	a, b := splitTwo("1 / 2", "/")
	if a != "1" || b != "2" {
		t.Fatalf("unexpected result: %q %q", a, b)
	}

	a, b = splitTwo("1", "/")
	if a != "1" || b != "" {
		t.Fatalf("unexpected fallback: %q %q", a, b)
	}
}

func TestParseUint(t *testing.T) {
	if parseUint("99") != 99 {
		t.Fatalf("parseUint failed")
	}
	if parseUint("not") != 0 {
		t.Fatalf("parseUint invalid should be zero")
	}
}
