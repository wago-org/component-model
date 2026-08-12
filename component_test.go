package component_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	component "github.com/wago-org/component-model"
	componentregister "github.com/wago-org/component-model/register"
	"github.com/wago-org/wago"
	wagoplugin "github.com/wago-org/wago/plugin"
)

// This fixture is a genuine Component Model binary, not a core Wasm module.
// It exercises nested instance exports and Canonical ABI lift/lower on Wago.
//
//go:embed testdata/adder.wasm
var adderWasm []byte

type registerFunc func(*wago.Registrar) error

func (f registerFunc) Register(reg *wago.Registrar) error { return f(reg) }

func testDefinition(id string) wago.PluginDefinition {
	return wago.PluginDefinition{
		ID:      id,
		Version: "1.0.0",
		Provenance: wago.PluginProvenance{
			Repository: "https://" + id,
			License:    "MIT",
		},
	}
}

func consumerProvider(ref **wagoplugin.Ref[component.Service]) wago.PluginProvider {
	definition := testDefinition("example.com/component-consumer")
	definition.Requires = []wago.PluginRequirement{{ID: component.PluginID, Version: "^0.1.0"}}
	definition.Consumes = []wago.ContractRequirement{{
		ID: component.Contract.ID(), Major: component.Contract.Major(), Mode: wago.ContractRequired,
	}}
	return wago.PluginProvider{
		Definition: definition,
		New: func() wago.Plugin {
			return registerFunc(func(reg *wago.Registrar) error {
				var err error
				*ref, err = wagoplugin.Require(reg, component.Contract)
				return err
			})
		},
	}
}

func pluginSet(t *testing.T, providers []wago.PluginProvider, config json.RawMessage) wago.PluginSet {
	t.Helper()
	set := wago.PluginSet{Providers: providers}
	for _, provider := range providers {
		digest, err := wago.DefinitionDigest(provider.Definition)
		if err != nil {
			t.Fatal(err)
		}
		selection := wago.PluginSelection{
			ID: provider.Definition.ID, DefinitionDigest: digest, Direct: true,
			Dependencies: map[string]string{},
		}
		for _, requirement := range provider.Definition.Requires {
			selection.Dependencies[requirement.ID] = requirement.Version
		}
		if provider.Definition.ID == component.PluginID {
			selection.Config = append(json.RawMessage(nil), config...)
			for _, request := range provider.Definition.Authorities {
				selection.Grants = append(selection.Grants, wago.AuthorityGrant{Name: request.Name, Scope: request.Scope})
			}
		}
		for _, requirement := range provider.Definition.Consumes {
			var owners []string
			for _, candidate := range providers {
				for _, provided := range candidate.Definition.Provides {
					if provided.ID == requirement.ID && provided.Major == requirement.Major {
						owners = append(owners, candidate.Definition.ID)
					}
				}
			}
			sort.Strings(owners)
			selection.Contracts = append(selection.Contracts, wago.ContractBinding{
				ID: requirement.ID, Major: requirement.Major, Providers: owners,
			})
		}
		set.Selections = append(set.Selections, selection)
	}
	return set
}

func loadService(t *testing.T, config json.RawMessage) (*wago.Runtime, *wagoplugin.Ref[component.Service]) {
	t.Helper()
	var ref *wagoplugin.Ref[component.Service]
	providers := []wago.PluginProvider{component.Provider(), consumerProvider(&ref)}
	rt := wago.NewRuntime()
	if err := rt.LoadPlugins(context.Background(), pluginSet(t, providers, config)); err != nil {
		_ = rt.Close()
		t.Fatal(err)
	}
	return rt, ref
}

func TestPluginExecutesComponentInsideContractLease(t *testing.T) {
	rt, ref := loadService(t, nil)
	defer rt.Close()

	var escaped *component.Instance
	err := ref.With(func(service component.Service) error {
		return service.WithInstance(context.Background(), adderWasm, func(in *component.Instance) error {
			escaped = in
			got, err := in.CallExport(context.Background(), "component:adder/calc", "add", uint32(2), uint32(3))
			if err != nil {
				return err
			}
			if len(got) != 1 || got[0] != uint32(5) {
				t.Fatalf("add(2, 3) = %#v, want [5]", got)
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := escaped.CallExport(context.Background(), "component:adder/calc", "add", uint32(1), uint32(1)); err == nil {
		t.Fatal("component instance escaped its WithInstance callback")
	}
}

func TestPluginOperatesWithinNarrowedInstantiationGrant(t *testing.T) {
	var ref *wagoplugin.Ref[component.Service]
	providers := []wago.PluginProvider{component.Provider(), consumerProvider(&ref)}
	set := pluginSet(t, providers, nil)
	for i := range set.Selections {
		if set.Selections[i].ID != component.PluginID {
			continue
		}
		for j := range set.Selections[i].Grants {
			if set.Selections[i].Grants[j].Name == wago.AuthorityCoreInstanceInstantiate {
				set.Selections[i].Grants[j].Scope = wago.AuthorityScope{MaxInstances: 1, MaxMemoryBytes: 4 << 30}
			}
		}
	}
	rt := wago.NewRuntime()
	defer rt.Close()
	if err := rt.LoadPlugins(context.Background(), set); err != nil {
		t.Fatal(err)
	}
	if err := ref.With(func(service component.Service) error {
		return service.WithInstance(context.Background(), adderWasm, func(in *component.Instance) error {
			_, err := in.CallExport(context.Background(), "component:adder/calc", "add", uint32(1), uint32(2))
			return err
		})
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPluginCompileCache(t *testing.T) {
	rt, ref := loadService(t, nil)
	cache := component.NewCompileCache()
	defer rt.Close()
	defer cache.Close(context.Background())

	for i := 0; i < 2; i++ {
		err := ref.With(func(service component.Service) error {
			return service.WithInstance(context.Background(), adderWasm, func(in *component.Instance) error {
				got, err := in.CallExport(context.Background(), "component:adder/calc", "add", uint32(10), uint32(20))
				if err != nil {
					return err
				}
				if len(got) != 1 || got[0] != uint32(30) {
					t.Fatalf("call #%d = %#v, want [30]", i, got)
				}
				return nil
			}, component.WithCompileCache(cache))
		})
		if err != nil {
			t.Fatalf("call #%d: %v", i, err)
		}
	}
}

func TestPluginShutdownWaitsForComponentCallbackAndRevokesContract(t *testing.T) {
	rt, ref := loadService(t, nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	callDone := make(chan error, 1)
	go func() {
		callDone <- ref.With(func(service component.Service) error {
			return service.WithInstance(context.Background(), adderWasm, func(*component.Instance) error {
				close(entered)
				<-release
				return nil
			})
		})
	}()
	<-entered
	closeDone := make(chan error, 1)
	go func() { closeDone <- rt.Close() }()
	select {
	case err := <-closeDone:
		t.Fatalf("runtime closed before callback returned: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-callDone; err != nil {
		t.Fatal(err)
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
	if err := ref.With(func(component.Service) error { return nil }); !errors.Is(err, wago.ErrPermissionDenied) {
		t.Fatalf("contract after close = %v, want permission denied", err)
	}
}

func TestPluginRejectsMissingAuthorityAndStrictConfig(t *testing.T) {
	providers := []wago.PluginProvider{component.Provider()}
	set := pluginSet(t, providers, nil)
	set.Selections[0].Grants = set.Selections[0].Grants[:2]
	rt := wago.NewRuntime()
	defer rt.Close()
	if err := rt.LoadPlugins(context.Background(), set); !errors.Is(err, wago.ErrPermissionDenied) {
		t.Fatalf("missing authority = %v, want permission denied", err)
	}

	for _, config := range []json.RawMessage{
		json.RawMessage(`{"unknown":true}`),
		json.RawMessage(`null`),
		json.RawMessage(`{} {}`),
	} {
		set := pluginSet(t, providers, config)
		if err := wago.ValidatePluginSet(set); err == nil {
			t.Fatalf("accepted invalid config %s", config)
		}
	}
}

func TestConsumerGraphRequiresPackageAndExactContractBinding(t *testing.T) {
	var ref *wagoplugin.Ref[component.Service]
	consumer := consumerProvider(&ref)
	if err := wago.ValidatePluginSet(pluginSet(t, []wago.PluginProvider{consumer}, nil)); err == nil {
		t.Fatal("accepted component consumer without its package and contract provider")
	}

	providers := []wago.PluginProvider{component.Provider(), consumer}
	set := pluginSet(t, providers, nil)
	for i := range set.Selections {
		if set.Selections[i].ID == consumer.Definition.ID {
			set.Selections[i].Contracts = nil
		}
	}
	if err := wago.ValidatePluginSet(set); err == nil {
		t.Fatal("accepted component consumer without its reviewed contract binding")
	}

	incompatible := consumerProvider(&ref)
	incompatible.Definition.Requires[0].Version = ">=1.0.0"
	if err := wago.ValidatePluginSet(pluginSet(t, []wago.PluginProvider{component.Provider(), incompatible}, nil)); err == nil {
		t.Fatal("accepted component provider outside the consumer's version range")
	}
}

func TestDefinitionUsesExactAuthoritiesAndVersionedContract(t *testing.T) {
	definition := component.Definition()
	if definition.ID != "github.com/wago-org/component-model" {
		t.Fatalf("plugin ID = %q", definition.ID)
	}
	if got, want := definition.Provides, []wago.ContractSpec{component.Contract.Spec()}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("provided contracts = %#v, want %#v", got, want)
	}
	wantAuthorities := []wago.Authority{
		wago.AuthorityCoreModuleCompile,
		wago.AuthorityCoreInstanceInstantiate,
		wago.AuthorityCoreFuncRefCreate,
	}
	if len(definition.Authorities) != len(wantAuthorities) {
		t.Fatalf("authorities = %#v", definition.Authorities)
	}
	for i, want := range wantAuthorities {
		if got := definition.Authorities[i]; got.Name != want || got.Mode != wago.AuthorityRequired {
			t.Fatalf("authority[%d] = %#v, want required %q", i, got, want)
		}
	}
	scope := definition.Authorities[1].Scope
	if scope.MaxInstances != 64 || scope.MaxMemoryBytes != 16<<30 {
		t.Fatalf("core instantiation scope = %#v, want 64 instances and 16 GiB", scope)
	}
}

func TestManifestMetadataMatchesProviderDefinition(t *testing.T) {
	raw, err := os.ReadFile("wago.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Schema  string `json:"$schema"`
		Package struct {
			Module      string            `json:"module"`
			Version     string            `json:"version"`
			Name        string            `json:"name"`
			Description string            `json:"description"`
			Stability   wago.Stability    `json:"stability"`
			License     string            `json:"license"`
			Repository  string            `json:"repository"`
			Homepage    string            `json:"homepage"`
			Engines     map[string]string `json:"engines"`
			Authors     []struct {
				Name string `json:"name"`
			} `json:"authors"`
		} `json:"package"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != "https://wago.sh/v1/schema.json" {
		t.Fatalf("manifest schema = %q", manifest.Schema)
	}
	definition := component.Definition()
	pkg := manifest.Package
	if pkg.Module != definition.ID || pkg.Version != definition.Version || pkg.Name != definition.Name ||
		pkg.Description != definition.Description || pkg.Stability != definition.Stability ||
		pkg.License != definition.Provenance.License || pkg.Repository != definition.Provenance.Repository ||
		pkg.Homepage != definition.Provenance.Homepage || len(pkg.Authors) != len(definition.Provenance.Authors) ||
		len(pkg.Authors) != 1 || pkg.Authors[0].Name != definition.Provenance.Authors[0] ||
		pkg.Engines["wago"] != definition.Compatibility.Engines["wago"] {
		t.Fatalf("manifest package metadata does not match provider definition: package=%#v definition=%#v", pkg, definition)
	}
	providers := componentregister.Providers()
	want, err := wago.EncodeProviderCatalog("github.com/wago-org/component-model/register", providers)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(wago.ProviderCatalogFile)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s is stale; run wago plugin catalog", wago.ProviderCatalogFile)
	}
	if _, err := wago.DecodeProviderCatalog(got); err != nil {
		t.Fatalf("%s: %v", wago.ProviderCatalogFile, err)
	}
}
