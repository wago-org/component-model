;; Adapted from Wasmtime tests/all/component_model/strings.rs at
;; 899e66bef961f63a795a371a19a1db019ef9e015.
;; Licensed under Apache-2.0 WITH LLVM-exception.
(component
  (component $receiver
    (core module $m
      (func (export "") (param i32 i32))
      (func (export "realloc") (param i32 i32 i32 i32) (result i32)
        i32.const 0)
      (memory (export "memory") 1)
    )
    (core instance $m (instantiate $m))
    (alias core export $m "realloc" (core func $realloc))
    (alias core export $m "memory" (core memory $memory))
    (func (export "accept") (param "value" string)
      (canon lift (core func $m "")
        (realloc $realloc)
        (memory $memory)
        string-encoding=utf8)
    )
  )

  (component $sender
    (import "accept" (func $accept (param "value" string)))
    (core module $memory
      (memory (export "memory") 1)
    )
    (core instance $memory (instantiate $memory))
    (alias core export $memory "memory" (core memory $linear-memory))
    (core func $accept (canon lower (func $accept)
      string-encoding=utf8
      (memory $linear-memory)))
    (core module $start
      (import "" "accept" (func $accept (param i32 i32)))
      (func $start
        (call $accept (i32.const 0x80000000) (i32.const 1)))
      (start $start)
    )
    (core instance (instantiate $start
      (with "" (instance (export "accept" (func $accept))))))
  )

  (instance $receiver (instantiate $receiver))
  (instance $sender (instantiate $sender
    (with "accept" (func $receiver "accept"))))
)
