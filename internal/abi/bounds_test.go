package abi

import (
	"math"
	"testing"

	bintype "github.com/wago-org/component-model/internal/binary"
)

func TestLoadAndStoreRejectWrappingRangesWithoutPanicking(t *testing.T) {
	primitives := []struct {
		name  string
		value Value
	}{
		{"u8", uint32(1)},
		{"u16", uint32(1)},
		{"u32", uint32(1)},
		{"u64", uint64(1)},
	}
	mem := make([]byte, 16)
	for ptr := uint32(math.MaxUint32 - 15); ; ptr++ {
		for _, tc := range primitives {
			t.Run(tc.name, func(t *testing.T) {
				typeDesc := bintype.PrimitiveDesc{Prim: tc.name}
				assertReturnsErrorWithoutPanic(t, func() error {
					_, err := Load(mem, ptr, typeDesc, nil)
					return err
				})
				assertReturnsErrorWithoutPanic(t, func() error {
					return Store(mem, ptr, typeDesc, tc.value, nil, Realloc{})
				})
			})
		}
		if ptr == math.MaxUint32 {
			break
		}
	}
}

func TestStringRangeRejectsPointerOverflowWithoutPanicking(t *testing.T) {
	assertReturnsErrorWithoutPanic(t, func() error {
		_, err := loadStringFromRange(make([]byte, 8), math.MaxUint32-3, 8)
		return err
	})
}

func TestCheckedByteLengthRejectsOverflow(t *testing.T) {
	if _, err := checkedByteLength(1<<30, 4); err == nil {
		t.Fatal("checkedByteLength accepted overflowing list byte length")
	}
}

func TestZeroSizedListRejectsExcessiveElementCount(t *testing.T) {
	_, err := loadListFromRange(nil, 0, maxListElements+1, bintype.RecordDesc{}, nil)
	if err == nil {
		t.Fatal("loadListFromRange accepted excessive zero-sized list")
	}
}

func TestCheckedLayoutArithmeticRejectsOverflow(t *testing.T) {
	if _, err := checkedAdd(math.MaxUint32, 1); err == nil {
		t.Fatal("checkedAdd accepted overflow")
	}
	if _, err := checkedAlign(math.MaxUint32, 8); err == nil {
		t.Fatal("checkedAlign accepted overflow")
	}
	if got := Align(math.MaxUint32, 8); got != math.MaxUint32 {
		t.Fatalf("Align overflow = %d, want MaxUint32 sentinel", got)
	}
}

func TestLayoutValidationRejectsRecursiveType(t *testing.T) {
	idx := uint32(0)
	record := bintype.RecordDesc{Fields: []bintype.RecordField{{Name: "next", Type: bintype.TypeRef{TypeIndex: &idx}}}}
	resolve := func(uint32) bintype.TypeDesc { return record }
	if err := validateLayoutType(record, resolve); err == nil {
		t.Fatal("recursive record was accepted")
	}
}

func TestLayoutValidationRejectsExcessiveDepth(t *testing.T) {
	types := make([]bintype.TypeDesc, maxLayoutTypeDepth+2)
	types[len(types)-1] = bintype.PrimitiveDesc{Prim: "u8"}
	for i := len(types) - 2; i >= 0; i-- {
		idx := uint32(i + 1)
		types[i] = bintype.OptionDesc{Element: bintype.TypeRef{TypeIndex: &idx}}
	}
	resolve := func(idx uint32) bintype.TypeDesc { return types[idx] }
	if err := validateLayoutType(types[0], resolve); err == nil {
		t.Fatal("excessively deep type was accepted")
	}
}

func assertReturnsErrorWithoutPanic(t *testing.T, fn func() error) {
	t.Helper()
	defer func() {
		if got := recover(); got != nil {
			t.Fatalf("panicked: %v", got)
		}
	}()
	if err := fn(); err == nil {
		t.Fatal("returned nil error")
	}
}

func FuzzLoadStorePrimitiveNeverPanics(f *testing.F) {
	for _, ptr := range []uint32{0, 1, math.MaxUint32 - 7, math.MaxUint32} {
		f.Add(ptr, byte(0))
	}
	primitives := []struct {
		name  string
		value Value
	}{
		{"u8", uint32(1)},
		{"u16", uint32(1)},
		{"u32", uint32(1)},
		{"u64", uint64(1)},
		{"char", rune('x')},
	}
	f.Fuzz(func(t *testing.T, ptr uint32, selector byte) {
		tc := primitives[int(selector)%len(primitives)]
		typeDesc := bintype.PrimitiveDesc{Prim: tc.name}
		mem := make([]byte, 64)
		_, _ = Load(mem, ptr, typeDesc, nil)
		_ = Store(mem, ptr, typeDesc, tc.value, nil, Realloc{})
	})
}
