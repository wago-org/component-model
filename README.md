# Wago Component Model

`component-model` is Wago's official WebAssembly Component Model plugin. It
decodes, links, and executes components while keeping the core Wago runtime
small and removable by Go's linker.

The plugin publishes the versioned service
`wago-org/component-model/runtime/v1`. WASI and other world plugins consume
that service instead of receiving core-runtime authority themselves. The only
privileged boundary is Wago's revocable `runtime.core` engine capability,
which exposes compilation, instantiation, and host-function references—not the
runtime object, plugin registry, policies, hooks, or lifecycle control.

## Use

```go
runtime := wago.NewRuntime()
defer runtime.Close()

components, err := component.Enable(runtime)
if err != nil {
	return err
}
instance, err := components.Instantiate(ctx, componentBytes, options...)
```

Plugin builds blank-import the conventional registration package:

```go
import _ "github.com/wago-org/component-model/register"
```

## Security

- Component execution authority is explicit, narrow, and revoked on shutdown.
- Plugin installation and typed service resolution are transactional.
- Missing capabilities, providers, and type matches fail before activation.
- Component decoding and Canonical ABI memory access are bounds checked.
- Guest-visible filesystem, network, environment, and process authority belong
  to separate world plugins such as `wago-org/wasi`; this plugin grants none.

## Development

```sh
go test ./...
go test -race ./...
go vet ./...
```

This project is licensed under Apache-2.0. See [NOTICE](NOTICE) for upstream
provenance.
