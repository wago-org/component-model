package component

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/wago-org/component-model/internal/engine"
	"github.com/wago-org/component-model/internal/instance"
	"github.com/wago-org/wago"
	wagoplugin "github.com/wago-org/wago/plugin"
)

// PluginID is the canonical Component Model plugin ID.
const PluginID = "github.com/wago-org/component-model"

const (
	// Wago charges an unbounded memory32 declaration at its finite 65,535-page
	// implementation reservation. Twenty GiB therefore admits five ordinary
	// unbounded-memory modules while the separate slot limit leaves room for
	// memoryless adapters and linker shims.
	requestedMaxCoreInstances   = 64
	requestedMaxCoreMemoryBytes = 20 << 30
)

// Contract is the major-versioned Component Model execution service consumed
// by WASI and other component-world plugins.
var Contract = wagoplugin.NewContract[Service](PluginID+"/runtime", 1)

// Service is the Component Model plugin's cross-plugin execution boundary.
// WithInstance keeps the service and every core resource it creates inside the
// caller's contract lease. The instance is closed before WithInstance returns
// and must not be retained by fn.
type Service interface {
	WithInstance(context.Context, []byte, func(*Instance) error, ...Option) error
}

var configSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "maxProperties": 0
}`)

// Definition returns fresh immutable metadata for the explicit provider.
func Definition() wago.PluginDefinition {
	return wago.PluginDefinition{
		ID:          PluginID,
		Name:        "Wago Component Model",
		Version:     "0.1.3",
		Description: "WebAssembly Component Model execution and Canonical ABI linking for Wago.",
		Stability:   wago.Experimental,
		Compatibility: wago.Compatibility{
			Engines: map[string]string{"wago": ">=0.1.0"},
		},
		Provenance: wago.PluginProvenance{
			Homepage:   "https://github.com/wago-org/component-model#readme",
			Repository: "https://github.com/wago-org/component-model",
			License:    "Apache-2.0",
			Authors:    []string{"Jairus Tanaka"},
		},
		Authorities: []wago.AuthorityRequest{
			{
				Name:   wago.AuthorityCoreModuleCompile,
				Mode:   wago.AuthorityRequired,
				Reason: "compile the core WebAssembly modules embedded in a component",
			},
			{
				Name:   wago.AuthorityCoreInstanceInstantiate,
				Mode:   wago.AuthorityRequired,
				Reason: "instantiate and own the bounded core-module graph behind a component instance",
				Scope: wago.AuthorityScope{
					MaxInstances:   requestedMaxCoreInstances,
					MaxMemoryBytes: requestedMaxCoreMemoryBytes,
				},
			},
			{
				Name:   wago.AuthorityCoreFuncRefCreate,
				Mode:   wago.AuthorityRequired,
				Reason: "bridge Canonical ABI lifts and lowers through typed host function references",
			},
		},
		ConfigSchema: append(json.RawMessage(nil), configSchema...),
		Provides:     []wago.ContractSpec{Contract.Spec()},
	}
}

// Provider is the side-effect-free catalog entry for Component Model support.
func Provider() wago.PluginProvider {
	return wago.PluginProvider{
		Definition:     Definition(),
		New:            func() wago.Plugin { return new(componentPlugin) },
		ValidateConfig: validateConfig,
	}
}

func validateConfig(raw json.RawMessage) error {
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var cfg struct{}
	if err := dec.Decode(&cfg); err != nil {
		return fmt.Errorf("component: config: %w", err)
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("component: config must be an object")
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("component: config has a trailing JSON value")
	}
	return nil
}

type componentPlugin struct{}

func (*componentPlugin) Register(reg *wago.Registrar) error {
	var cfg struct{}
	if err := reg.Config(&cfg); err != nil {
		return err
	}
	compiler, err := reg.CoreModuleCompiler()
	if err != nil {
		return err
	}
	instantiator, err := reg.CoreInstanceInstantiator()
	if err != nil {
		return err
	}
	funcrefs, err := reg.CoreFuncRefFactory()
	if err != nil {
		return err
	}
	service := &runtimeService{engine: engine.Wrap(compiler, instantiator, funcrefs)}
	return wagoplugin.Provide(reg, Contract, Service(service))
}

type runtimeService struct {
	engine engine.Runtime
}

func (r *runtimeService) WithInstance(ctx context.Context, componentBytes []byte, fn func(*Instance) error, opts ...Option) (err error) {
	if r == nil || r.engine == nil {
		return fmt.Errorf("component: inactive component service")
	}
	if ctx == nil {
		return fmt.Errorf("component: nil context")
	}
	if fn == nil {
		return fmt.Errorf("component: nil instance callback")
	}
	in, err := instance.Instantiate(ctx, r.engine, componentBytes, opts...)
	if err != nil {
		return err
	}
	closeCtx := context.WithoutCancel(ctx)
	defer func() { err = errors.Join(err, in.Close(closeCtx)) }()
	return fn(in)
}
