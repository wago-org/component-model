package binary

import (
	"bytes"
	"strings"
	"testing"

	"github.com/wago-org/component-model/internal/leb128"
)

func TestDecodeLimitsInputBytes(t *testing.T) {
	_, err := DecodeWithLimits(bytes.NewReader(make([]byte, 9)), DecodeLimits{MaxInputBytes: 8, MaxDepth: 4, MaxSections: 16})
	if err == nil || !strings.Contains(err.Error(), "input byte limit") {
		t.Fatalf("DecodeWithLimits error = %v, want input byte limit", err)
	}
}

func TestDecodeLimitsNestedDepth(t *testing.T) {
	component := nestedComponentBytes(5)
	_, err := DecodeWithLimits(bytes.NewReader(component), DecodeLimits{MaxInputBytes: uint64(len(component)), MaxDepth: 4, MaxSections: 16})
	if err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("DecodeWithLimits error = %v, want nesting depth", err)
	}
}

func TestDecodeLimitsAggregateSections(t *testing.T) {
	component := append([]byte{}, componentPreambleForTest()...)
	component = append(component, 0, 0, 0, 0, 0, 0) // three empty custom sections
	_, err := DecodeWithLimits(bytes.NewReader(component), DecodeLimits{MaxInputBytes: uint64(len(component)), MaxDepth: 4, MaxSections: 2})
	if err == nil || !strings.Contains(err.Error(), "section limit") {
		t.Fatalf("DecodeWithLimits error = %v, want section limit", err)
	}
}

func nestedComponentBytes(depth int) []byte {
	b := componentPreambleForTest()
	for range depth {
		wrapped := append([]byte{}, componentPreambleForTest()...)
		wrapped = append(wrapped, 4)
		wrapped = append(wrapped, leb128.EncodeUint32(uint32(len(b)))...)
		wrapped = append(wrapped, b...)
		b = wrapped
	}
	return b
}

func componentPreambleForTest() []byte {
	return []byte{0x00, 0x61, 0x73, 0x6d, 0x0d, 0x00, 0x01, 0x00}
}
