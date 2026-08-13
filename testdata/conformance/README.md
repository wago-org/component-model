# Component Model reference tests

This directory vendors every `.wast` file from the official
`WebAssembly/component-model` reference suite at commit
`8b5c200fe8ef39d715849d4b9b5e03017c8ab62a`.

`upstream/` is the unmodified source corpus. `generated/` and `manifest.json`
are produced by `tools/wast2manifest`, using the upstream `wast` parser rather
than a local S-expression splitter so component definitions, instances,
quoted binaries, value literals, and source locations retain official WAST
semantics.

The generated files are checked in. Normal Go builds and tests do not require
Rust or `wasm-tools`.

`wasmtime/` similarly vendors and converts all 79 additional WAST regression
files from Wasmtime's `tests/misc_testsuite/component-model` directory at
commit `e67ea9a663adc718b00389dba2d7b899a17032c0`. Wasmtime's copy of the
official reference suite is not duplicated because it is already represented
by `upstream/`.

To regenerate with Rust installed:

```sh
cargo run --locked --manifest-path tools/wast2manifest/Cargo.toml -- \
  testdata/conformance/upstream \
  testdata/conformance \
  8b5c200fe8ef39d715849d4b9b5e03017c8ab62a
```

Regeneration is deterministic for one source revision and `Cargo.lock`.

## Coverage status

The official corpus currently contributes 892 component/validation cases and
524 calls. The Wasmtime-specific corpus contributes another 447 cases and 346
calls. Tests execute supported behavior and maintain assertion-level expected
failure ledgers in `xfail.json`; an expected failure that starts passing fails
the suite until its ledger entry is removed. Unsupported parent instances are
still represented in the manifest, so their child assertions become runnable
without another fixture import once the parent feature lands.
