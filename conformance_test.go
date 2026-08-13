package component_test

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	component "github.com/wago-org/component-model"
	"github.com/wago-org/component-model/internal/binary"
)

//go:embed testdata/conformance/manifest.json testdata/conformance/xfail.json testdata/conformance/generated/*.wasm testdata/conformance/wasmtime/manifest.json testdata/conformance/wasmtime/xfail.json testdata/conformance/wasmtime/generated/*.wasm
var conformanceFixtures embed.FS

type conformanceManifest struct {
	Revision string            `json:"revision"`
	Files    []conformanceFile `json:"files"`
}

type conformanceFile struct {
	Source string            `json:"source"`
	Cases  []conformanceCase `json:"cases"`
}

type conformanceCase struct {
	ID              string              `json:"id"`
	Line            int                 `json:"line"`
	Kind            string              `json:"kind"`
	Wasm            string              `json:"wasm"`
	Message         string              `json:"message"`
	GenerationError string              `json:"generation_error"`
	Actions         []conformanceAction `json:"actions"`
}

type conformanceAction struct {
	Line            int                `json:"line"`
	Kind            string             `json:"kind"`
	Export          string             `json:"export"`
	Args            []conformanceValue `json:"args"`
	Results         []conformanceValue `json:"results"`
	Message         string             `json:"message"`
	GenerationError string             `json:"generation_error"`
}

type conformanceValue struct {
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value"`
}

type conformanceRecordField struct {
	Name  string           `json:"name"`
	Value conformanceValue `json:"value"`
}

type conformanceVariant struct {
	Case    string            `json:"case"`
	Payload *conformanceValue `json:"payload"`
}

type conformanceResult struct {
	IsErr   bool              `json:"is_err"`
	Payload *conformanceValue `json:"payload"`
}

func TestOfficialComponentModelSynchronousConformance(t *testing.T) {
	runComponentModelConformance(t, "testdata/conformance", func(source string) bool {
		return !strings.HasPrefix(source, "async/")
	})
}

func TestComponentModelConformanceCorpusIsComplete(t *testing.T) {
	official := readConformanceManifest(t, "testdata/conformance/manifest.json")
	wasmtime := readConformanceManifest(t, "testdata/conformance/wasmtime/manifest.json")
	assertConformanceCorpusCount(t, "official", official, 62, 892, 524)
	assertConformanceCorpusCount(t, "wasmtime", wasmtime, 79, 447, 346)
}

func assertConformanceCorpusCount(t *testing.T, name string, manifest conformanceManifest, wantFiles, wantCases, wantActions int) {
	t.Helper()
	var cases, actions int
	for _, file := range manifest.Files {
		cases += len(file.Cases)
		for _, testCase := range file.Cases {
			actions += len(testCase.Actions)
		}
	}
	if len(manifest.Files) != wantFiles || cases != wantCases || actions != wantActions {
		t.Fatalf("%s corpus = %d files, %d cases, %d actions; want %d, %d, %d", name, len(manifest.Files), cases, actions, wantFiles, wantCases, wantActions)
	}
}

func TestOfficialComponentModelAsyncConformance(t *testing.T) {
	runComponentModelConformance(t, "testdata/conformance", func(source string) bool {
		return strings.HasPrefix(source, "async/")
	})
}

func TestWasmtimeWastSynchronousConformance(t *testing.T) {
	runComponentModelConformance(t, "testdata/conformance/wasmtime", func(source string) bool {
		return !strings.HasPrefix(source, "async/")
	})
}

func TestWasmtimeWastAsyncConformance(t *testing.T) {
	runComponentModelConformance(t, "testdata/conformance/wasmtime", func(source string) bool {
		return strings.HasPrefix(source, "async/")
	})
}

func runComponentModelConformance(t *testing.T, fixtureRoot string, include func(string) bool) {
	t.Helper()
	manifest := readConformanceManifest(t, fixtureRoot+"/manifest.json")
	xfails := readConformanceXfails(t, fixtureRoot+"/xfail.json")
	runtime, components := loadService(t, nil)
	defer runtime.Close()

	err := components.With(func(service component.Service) error {
		for _, file := range manifest.Files {
			if !include(file.Source) {
				continue
			}
			file := file
			t.Run(file.Source, func(t *testing.T) {
				for _, testCase := range file.Cases {
					testCase := testCase
					t.Run(fmt.Sprintf("L%d/%s", testCase.Line, testCase.Kind), func(t *testing.T) {
						runConformanceCase(t, service, fixtureRoot, testCase, xfails)
					})
				}
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("component service lease: %v", err)
	}
}

func readConformanceManifest(t *testing.T, path string) conformanceManifest {
	t.Helper()
	data, err := conformanceFixtures.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest conformanceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func readConformanceXfails(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := conformanceFixtures.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	xfails := map[string]string{}
	if err := json.Unmarshal(data, &xfails); err != nil {
		t.Fatal(err)
	}
	return xfails
}

func runConformanceCase(t *testing.T, service component.Service, fixtureRoot string, testCase conformanceCase, xfails map[string]string) {
	t.Helper()
	if testCase.GenerationError != "" {
		finishConformanceCheck(t, testCase.ID, xfails, fmt.Errorf("fixture generation: %s", testCase.GenerationError))
		return
	}
	wasm, err := conformanceFixtures.ReadFile(fixtureRoot + "/" + testCase.Wasm)
	if err != nil {
		t.Fatal(err)
	}

	switch testCase.Kind {
	case "definition":
		_, err := binary.Decode(bytes.NewReader(wasm))
		finishConformanceCheck(t, testCase.ID, xfails, errorWithContext(err, "decode valid component definition"))
	case "assert-invalid", "assert-malformed":
		_, decodeErr := binary.Decode(bytes.NewReader(wasm))
		var checkErr error
		if decodeErr == nil {
			checkErr = fmt.Errorf("accepted component, want rejection containing %q", testCase.Message)
		}
		finishConformanceCheck(t, testCase.ID, xfails, checkErr)
	case "assert-instantiation-trap", "assert-unlinkable":
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		instantiateErr := service.WithInstance(ctx, wasm, func(*component.Instance) error { return nil })
		var checkErr error
		if instantiateErr == nil {
			checkErr = fmt.Errorf("instantiation succeeded, want trap containing %q", testCase.Message)
		}
		finishConformanceCheck(t, testCase.ID, xfails, checkErr)
	case "instantiate":
		if _, known := xfails[testCase.ID]; known {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			instantiateErr := service.WithInstance(ctx, wasm, func(*component.Instance) error { return nil })
			finishConformanceCheck(t, testCase.ID, xfails, errorWithContext(instantiateErr, "instantiate component"))
			return
		}
		runConformanceInstance(t, service, wasm, testCase, xfails)
	default:
		t.Fatalf("unsupported manifest case kind %q", testCase.Kind)
	}
}

func runConformanceInstance(t *testing.T, service component.Service, wasm []byte, testCase conformanceCase, xfails map[string]string) {
	t.Helper()
	decoded, decodeErr := binary.Decode(bytes.NewReader(wasm))
	if decodeErr != nil {
		t.Fatalf("decode component: %v", decodeErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := service.WithInstance(ctx, wasm, func(instance *component.Instance) error {
		for _, action := range testCase.Actions {
			action := action
			actionID := fmt.Sprintf("%s/action/L%d", testCase.ID, action.Line)
			t.Run(fmt.Sprintf("L%d/%s/%s", action.Line, action.Kind, action.Export), func(t *testing.T) {
				checkErr := checkConformanceAction(ctx, decoded, instance, action)
				finishConformanceCheck(t, actionID, xfails, checkErr)
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("instantiate component: %v", err)
	}
}

func checkConformanceAction(ctx context.Context, decoded *binary.Component, instance *component.Instance, action conformanceAction) error {
	if action.GenerationError != "" {
		return fmt.Errorf("fixture generation: %s", action.GenerationError)
	}
	funcDesc, typeErr := conformanceExportFunc(decoded, action.Export)
	var args []component.Value
	var err error
	if typeErr == nil {
		args, err = conformanceArgs(decoded, funcDesc, action.Args)
	} else {
		args, err = conformanceGenericValues(action.Args)
	}
	if err != nil {
		return fmt.Errorf("arguments: %w", err)
	}
	got, callErr := instance.Call(ctx, action.Export, args...)
	switch action.Kind {
	case "invoke":
		return errorWithContext(callErr, "invoke")
	case "assert-trap":
		if callErr == nil {
			return fmt.Errorf("call succeeded, want trap containing %q", action.Message)
		}
		return nil
	case "assert-return":
		if callErr != nil {
			return fmt.Errorf("call: %w", callErr)
		}
		var want []component.Value
		if typeErr == nil {
			want, err = conformanceResults(decoded, funcDesc, action.Results)
		} else {
			want, err = conformanceGenericValues(action.Results)
		}
		if err != nil {
			return fmt.Errorf("expected results: %w", err)
		}
		if !conformanceValuesEqual(got, want) {
			return fmt.Errorf("results = %#v, want %#v", got, want)
		}
		return nil
	default:
		return fmt.Errorf("unsupported manifest action kind %q", action.Kind)
	}
}

func finishConformanceCheck(t *testing.T, id string, xfails map[string]string, checkErr error) {
	t.Helper()
	if reason, known := xfails[id]; known {
		if checkErr == nil {
			t.Fatalf("unexpected conformance pass; remove xfail %q", reason)
		}
		t.Skipf("upstream case not yet supported: %s: %v", reason, checkErr)
	}
	if checkErr != nil {
		t.Fatal(checkErr)
	}
}

func errorWithContext(err error, context string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", context, err)
}

func conformanceExportFunc(comp *binary.Component, name string) (binary.FuncDesc, error) {
	for _, export := range comp.Exports {
		if export.Name == name && export.ExternType == 0x01 {
			return conformanceFuncAt(comp, export.ExternIndex, 0)
		}
	}
	return binary.FuncDesc{}, fmt.Errorf("component func export %q not found", name)
}

func conformanceFuncAt(comp *binary.Component, idx uint32, depth int) (binary.FuncDesc, error) {
	if depth > 64 || int(idx) >= len(comp.ComponentFuncSpace) {
		return binary.FuncDesc{}, fmt.Errorf("component func index %d is unresolved", idx)
	}
	entry := comp.ComponentFuncSpace[idx]
	var typeIdx uint32
	switch entry.Kind {
	case binary.ComponentFuncFromCanonLift:
		if int(entry.Canon) >= len(comp.Canons) {
			return binary.FuncDesc{}, fmt.Errorf("canon index %d is out of range", entry.Canon)
		}
		typeIdx = comp.Canons[entry.Canon].TypeIdx
	case binary.ComponentFuncFromImport:
		if int(entry.Import) >= len(comp.Imports) {
			return binary.FuncDesc{}, fmt.Errorf("import index %d is out of range", entry.Import)
		}
		typeIdx = comp.Imports[entry.Import].ExternIndex
	case binary.ComponentFuncFromExport:
		if int(entry.Export) >= len(comp.Exports) {
			return binary.FuncDesc{}, fmt.Errorf("export index %d is out of range", entry.Export)
		}
		return conformanceFuncAt(comp, comp.Exports[entry.Export].ExternIndex, depth+1)
	default:
		return binary.FuncDesc{}, fmt.Errorf("component func export resolves through unsupported alias")
	}
	desc, err := comp.ResolveType(typeIdx)
	if err != nil {
		return binary.FuncDesc{}, err
	}
	fn, ok := desc.(binary.FuncDesc)
	if !ok {
		return binary.FuncDesc{}, fmt.Errorf("component func type %d is %T", typeIdx, desc)
	}
	return fn, nil
}

func conformanceArgs(comp *binary.Component, fn binary.FuncDesc, values []conformanceValue) ([]component.Value, error) {
	if len(values) != len(fn.Params) {
		return nil, fmt.Errorf("got %d values for %d parameters", len(values), len(fn.Params))
	}
	out := make([]component.Value, len(values))
	for i := range values {
		value, err := conformanceValueForType(comp, fn.Params[i].Type, values[i])
		if err != nil {
			return nil, fmt.Errorf("parameter %d: %w", i, err)
		}
		out[i] = value
	}
	return out, nil
}

func conformanceResults(comp *binary.Component, fn binary.FuncDesc, values []conformanceValue) ([]component.Value, error) {
	refs := make([]binary.TypeRef, 0, len(fn.Results.Named)+1)
	if fn.Results.Unnamed != nil {
		refs = append(refs, *fn.Results.Unnamed)
	} else {
		for _, result := range fn.Results.Named {
			refs = append(refs, result.Type)
		}
	}
	if len(values) != len(refs) {
		return nil, fmt.Errorf("got %d values for %d results", len(values), len(refs))
	}
	out := make([]component.Value, len(values))
	for i := range values {
		value, err := conformanceValueForType(comp, refs[i], values[i])
		if err != nil {
			return nil, fmt.Errorf("result %d: %w", i, err)
		}
		out[i] = value
	}
	return out, nil
}

func conformanceGenericValues(values []conformanceValue) ([]component.Value, error) {
	out := make([]component.Value, len(values))
	for i := range values {
		value, err := conformanceGenericValue(values[i])
		if err != nil {
			return nil, fmt.Errorf("value %d: %w", i, err)
		}
		out[i] = value
	}
	return out, nil
}

func conformanceGenericValue(value conformanceValue) (component.Value, error) {
	switch value.Kind {
	case "bool", "u8", "s8", "u16", "s16", "u32", "s32", "u64", "s64", "f32", "f64", "char", "string":
		return conformancePrimitive(value, value.Kind)
	case "list", "tuple":
		var values []conformanceValue
		if err := json.Unmarshal(value.Value, &values); err != nil {
			return nil, err
		}
		return conformanceGenericValues(values)
	case "record":
		var fields []conformanceRecordField
		if err := json.Unmarshal(value.Value, &fields); err != nil {
			return nil, err
		}
		values := make([]conformanceValue, len(fields))
		for i := range fields {
			values[i] = fields[i].Value
		}
		return conformanceGenericValues(values)
	case "option":
		if string(value.Value) == "null" {
			return nil, nil
		}
		var inner conformanceValue
		if err := json.Unmarshal(value.Value, &inner); err != nil {
			return nil, err
		}
		return conformanceGenericValue(inner)
	case "result":
		var result conformanceResult
		if err := json.Unmarshal(value.Value, &result); err != nil {
			return nil, err
		}
		var payload component.Value
		var err error
		if result.Payload != nil {
			payload, err = conformanceGenericValue(*result.Payload)
			if err != nil {
				return nil, err
			}
		}
		return component.ResultValue{IsErr: result.IsErr, Payload: payload}, nil
	default:
		return nil, fmt.Errorf("type information is required for %q", value.Kind)
	}
}

func conformanceValueForType(comp *binary.Component, ref binary.TypeRef, value conformanceValue) (component.Value, error) {
	desc, err := conformanceResolveType(comp, ref)
	if err != nil {
		return nil, err
	}
	switch typed := desc.(type) {
	case binary.PrimitiveDesc:
		return conformancePrimitive(value, typed.Prim)
	case binary.ListDesc:
		var values []conformanceValue
		if err := json.Unmarshal(value.Value, &values); err != nil || value.Kind != "list" {
			return nil, fmt.Errorf("expected list, got %q", value.Kind)
		}
		out := make([]component.Value, len(values))
		for i := range values {
			out[i], err = conformanceValueForType(comp, typed.Element, values[i])
			if err != nil {
				return nil, fmt.Errorf("list element %d: %w", i, err)
			}
		}
		return out, nil
	case binary.TupleDesc:
		var values []conformanceValue
		if err := json.Unmarshal(value.Value, &values); err != nil || value.Kind != "tuple" {
			return nil, fmt.Errorf("expected tuple, got %q", value.Kind)
		}
		if len(values) != len(typed.Elements) {
			return nil, fmt.Errorf("tuple has %d values, want %d", len(values), len(typed.Elements))
		}
		out := make([]component.Value, len(values))
		for i := range values {
			out[i], err = conformanceValueForType(comp, typed.Elements[i], values[i])
			if err != nil {
				return nil, fmt.Errorf("tuple element %d: %w", i, err)
			}
		}
		return out, nil
	case binary.RecordDesc:
		var fields []conformanceRecordField
		if err := json.Unmarshal(value.Value, &fields); err != nil || value.Kind != "record" {
			return nil, fmt.Errorf("expected record, got %q", value.Kind)
		}
		byName := make(map[string]conformanceValue, len(fields))
		for _, field := range fields {
			byName[field.Name] = field.Value
		}
		out := make([]component.Value, len(typed.Fields))
		for i, field := range typed.Fields {
			input, ok := byName[field.Name]
			if !ok {
				return nil, fmt.Errorf("record field %q is missing", field.Name)
			}
			out[i], err = conformanceValueForType(comp, field.Type, input)
			if err != nil {
				return nil, fmt.Errorf("record field %q: %w", field.Name, err)
			}
		}
		return out, nil
	case binary.VariantDesc:
		var variant conformanceVariant
		if err := json.Unmarshal(value.Value, &variant); err != nil || value.Kind != "variant" {
			return nil, fmt.Errorf("expected variant, got %q", value.Kind)
		}
		for i, variantCase := range typed.Cases {
			if variantCase.Name != variant.Case {
				continue
			}
			var payload component.Value
			if variantCase.Type != nil {
				if variant.Payload == nil {
					return nil, fmt.Errorf("variant case %q requires a payload", variant.Case)
				}
				payload, err = conformanceValueForType(comp, *variantCase.Type, *variant.Payload)
				if err != nil {
					return nil, err
				}
			}
			return component.VariantValue{Disc: uint32(i), Payload: payload}, nil
		}
		return nil, fmt.Errorf("variant case %q not found", variant.Case)
	case binary.EnumDesc:
		var name string
		if err := json.Unmarshal(value.Value, &name); err != nil || value.Kind != "enum" {
			return nil, fmt.Errorf("expected enum, got %q", value.Kind)
		}
		for i, enumCase := range typed.Cases {
			if enumCase == name {
				return uint32(i), nil
			}
		}
		return nil, fmt.Errorf("enum case %q not found", name)
	case binary.FlagsDesc:
		var names []string
		if err := json.Unmarshal(value.Value, &names); err != nil || value.Kind != "flags" {
			return nil, fmt.Errorf("expected flags, got %q", value.Kind)
		}
		var bits uint32
		for _, name := range names {
			found := false
			for i, flag := range typed.Names {
				if flag == name {
					if i >= 32 {
						return nil, fmt.Errorf("flag %q exceeds the current uint32 representation", name)
					}
					bits |= 1 << i
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("flag %q not found", name)
			}
		}
		return bits, nil
	case binary.OptionDesc:
		if value.Kind != "option" {
			return nil, fmt.Errorf("expected option, got %q", value.Kind)
		}
		if string(value.Value) == "null" {
			return nil, nil
		}
		var inner conformanceValue
		if err := json.Unmarshal(value.Value, &inner); err != nil {
			return nil, err
		}
		return conformanceValueForType(comp, typed.Element, inner)
	case binary.ResultDesc:
		var result conformanceResult
		if err := json.Unmarshal(value.Value, &result); err != nil || value.Kind != "result" {
			return nil, fmt.Errorf("expected result, got %q", value.Kind)
		}
		var payload component.Value
		arm := typed.Ok
		if result.IsErr {
			arm = typed.Err
		}
		if arm != nil {
			if result.Payload == nil {
				return nil, fmt.Errorf("result arm requires a payload")
			}
			payload, err = conformanceValueForType(comp, *arm, *result.Payload)
			if err != nil {
				return nil, err
			}
		}
		return component.ResultValue{IsErr: result.IsErr, Payload: payload}, nil
	case binary.OwnDesc, binary.BorrowDesc, binary.StreamDesc, binary.FutureDesc:
		return conformancePrimitive(value, "u32")
	default:
		return nil, fmt.Errorf("unsupported conformance value type %T", desc)
	}
}

func conformanceResolveType(comp *binary.Component, ref binary.TypeRef) (binary.TypeDesc, error) {
	if ref.Primitive != "" {
		return binary.PrimitiveDesc{Prim: ref.Primitive}, nil
	}
	if ref.TypeIndex == nil {
		return nil, fmt.Errorf("empty type reference")
	}
	return comp.ResolveType(*ref.TypeIndex)
}

func conformancePrimitive(value conformanceValue, primitive string) (component.Value, error) {
	if value.Kind != primitive {
		return nil, fmt.Errorf("expected %s, got %s", primitive, value.Kind)
	}
	switch primitive {
	case "bool":
		var out bool
		if err := json.Unmarshal(value.Value, &out); err != nil {
			return nil, err
		}
		return out, nil
	case "u8", "u16", "u32", "error-context":
		var out uint32
		if err := json.Unmarshal(value.Value, &out); err != nil {
			return nil, err
		}
		return out, nil
	case "s8", "s16", "s32":
		var out int32
		if err := json.Unmarshal(value.Value, &out); err != nil {
			return nil, err
		}
		return out, nil
	case "u64":
		var text string
		if err := json.Unmarshal(value.Value, &text); err != nil {
			return nil, err
		}
		return strconv.ParseUint(text, 10, 64)
	case "s64":
		var text string
		if err := json.Unmarshal(value.Value, &text); err != nil {
			return nil, err
		}
		return strconv.ParseInt(text, 10, 64)
	case "f32":
		var text string
		if err := json.Unmarshal(value.Value, &text); err != nil {
			return nil, err
		}
		bits, err := strconv.ParseUint(text, 16, 32)
		return math.Float32frombits(uint32(bits)), err
	case "f64":
		var text string
		if err := json.Unmarshal(value.Value, &text); err != nil {
			return nil, err
		}
		bits, err := strconv.ParseUint(text, 16, 64)
		return math.Float64frombits(bits), err
	case "char":
		var text string
		if err := json.Unmarshal(value.Value, &text); err != nil {
			return nil, err
		}
		runes := []rune(text)
		if len(runes) != 1 {
			return nil, fmt.Errorf("char has %d runes", len(runes))
		}
		return runes[0], nil
	case "string":
		var out string
		if err := json.Unmarshal(value.Value, &out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported primitive %q", primitive)
	}
}

func conformanceValuesEqual(got, want []component.Value) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !conformanceValueEqual(got[i], want[i]) {
			return false
		}
	}
	return true
}

func conformanceValueEqual(got, want component.Value) bool {
	switch want := want.(type) {
	case float32:
		got, ok := got.(float32)
		return ok && math.Float32bits(got) == math.Float32bits(want)
	case float64:
		got, ok := got.(float64)
		return ok && math.Float64bits(got) == math.Float64bits(want)
	case []component.Value:
		got, ok := got.([]component.Value)
		return ok && conformanceValuesEqual(got, want)
	case component.VariantValue:
		got, ok := got.(component.VariantValue)
		return ok && got.Disc == want.Disc && conformanceValueEqual(got.Payload, want.Payload)
	case component.ResultValue:
		got, ok := got.(component.ResultValue)
		return ok && got.IsErr == want.IsErr && conformanceValueEqual(got.Payload, want.Payload)
	default:
		return reflect.DeepEqual(got, want)
	}
}
