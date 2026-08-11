;; Adapted from Wasmtime tests/all/component_model/post_return.rs at
;; 899e66bef961f63a795a371a19a1db019ef9e015.
;; Licensed under Apache-2.0 WITH LLVM-exception.
(component
  (core module $m
    (memory (export "memory") 1)
    (func (export "get") (result i32)
      (i32.store offset=0 (i32.const 8) (i32.const 100))
      (i32.store offset=4 (i32.const 8) (i32.const 11))
      i32.const 8)
    (func (export "post") (param i32)
      local.get 0 i32.const 8 i32.ne if unreachable end)
    (data (i32.const 100) "hello world")
  )
  (core instance $i (instantiate $m))
  (alias core export $i "memory" (core memory $memory))
  (alias core export $i "post" (core func $post))
  (func (export "get") (result string)
    (canon lift (core func $i "get")
      (post-return $post)
      (memory $memory)))
)
