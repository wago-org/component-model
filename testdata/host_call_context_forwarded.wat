(component
  (import "test:context/host" (instance $host
    (export "wait" (func))))
  (alias export $host "wait" (func $wait))
  (core func $wait (canon lower (func $wait)))
  (core module $callee
    (import "host" "wait" (func $wait))
    (func (export "forward") (result i32)
      call $wait
      i32.const 7))
  (core module $caller
    (import "callee" "forward" (func $forward (result i32)))
    (func (export "run") (result i32)
      call $forward))
  (core instance $host (export "wait" (func $wait)))
  (core instance $callee (instantiate $callee (with "host" (instance $host))))
  (core instance $caller (instantiate $caller (with "callee" (instance $callee))))
  (func (export "run") (result u32) (canon lift (core func $caller "run")))
)
