;; Adapted from Wasmtime tests/all/component_model/post_return.rs at
;; 899e66bef961f63a795a371a19a1db019ef9e015.
;; Licensed under Apache-2.0 WITH LLVM-exception.
(component
  (core module $m
    (func (export "call"))
    (func (export "post") unreachable)
  )
  (core instance $i (instantiate $m))
  (alias core export $i "post" (core func $post))
  (func (export "call")
    (canon lift (core func $i "call") (post-return $post)))
)
