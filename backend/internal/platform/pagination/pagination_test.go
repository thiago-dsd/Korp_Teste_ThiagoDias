package pagination_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/thiagodias/korp-invoices/internal/platform/pagination"
)

func TestEncodeAndDecodeRoundTrip(t *testing.T) {
	cursor := pagination.Cursor{Key: "P-1", ID: "8e5b6f4e-0e5a-4f0e-9d3a-2b1c4d5e6f70"}

	decoded, err := pagination.Decode(pagination.Encode(cursor))
	if err != nil {
		t.Fatalf("Decode() returned error: %v", err)
	}
	if decoded != cursor {
		t.Errorf("decoded = %+v, want %+v", decoded, cursor)
	}
}

func TestEncodeProducesAnOpaqueValue(t *testing.T) {
	encoded := pagination.Encode(pagination.Cursor{Key: "P-1"})

	if strings.Contains(encoded, "P-1") {
		t.Errorf("cursor = %q, want the key not to be readable", encoded)
	}
	if strings.ContainsAny(encoded, "+/=") {
		t.Errorf("cursor = %q, want it safe to put in a query string", encoded)
	}
}

func TestDecodeAcceptsAnEmptyCursorAsTheFirstPage(t *testing.T) {
	cursor, err := pagination.Decode("")
	if err != nil {
		t.Fatalf("Decode(\"\") returned error: %v", err)
	}
	if cursor.Key != "" {
		t.Errorf("cursor = %+v, want an empty one", cursor)
	}
}

func TestDecodeRejectsCursorsItDidNotProduce(t *testing.T) {
	for _, raw := range []string{"not-base64!", "e30", "bm90LWpzb24", "eyJrIjoiIn0"} {
		if _, err := pagination.Decode(raw); !errors.Is(err, pagination.ErrInvalidCursor) {
			t.Errorf("Decode(%q) error = %v, want ErrInvalidCursor", raw, err)
		}
	}
}

func TestNormalizeLimit(t *testing.T) {
	tests := map[int]int{
		0:                        pagination.DefaultLimit,
		-5:                       pagination.DefaultLimit,
		10:                       10,
		pagination.MaxLimit:      pagination.MaxLimit,
		pagination.MaxLimit + 50: pagination.MaxLimit,
	}

	for input, want := range tests {
		if got := pagination.NormalizeLimit(input); got != want {
			t.Errorf("NormalizeLimit(%d) = %d, want %d", input, got, want)
		}
	}
}

func TestParseLimit(t *testing.T) {
	if limit, err := pagination.ParseLimit(""); err != nil || limit != pagination.DefaultLimit {
		t.Errorf("ParseLimit(\"\") = %d, %v, want the default", limit, err)
	}
	if limit, err := pagination.ParseLimit("25"); err != nil || limit != 25 {
		t.Errorf("ParseLimit(\"25\") = %d, %v, want 25", limit, err)
	}
	if limit, err := pagination.ParseLimit("1000"); err != nil || limit != pagination.MaxLimit {
		t.Errorf("ParseLimit(\"1000\") = %d, %v, want the maximum", limit, err)
	}
	for _, raw := range []string{"abc", "-1"} {
		if _, err := pagination.ParseLimit(raw); err == nil {
			t.Errorf("ParseLimit(%q) accepted the value, want an error", raw)
		}
	}
}
