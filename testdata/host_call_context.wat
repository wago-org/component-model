(component
  (import "test:context/host" (instance $host
    (export "wait" (func))))
  (alias export $host "wait" (func $wait))
  (core func $wait (canon lower (func $wait)))
  (core module $guest
    (import "host" "wait" (func $wait))
    (func (export "run") (result i32)
      call $wait
      i32.const 7))
  (core instance $host (export "wait" (func $wait)))
  (core instance $guest (instantiate $guest (with "host" (instance $host))))
  (func (export "run") (result u32) (canon lift (core func $guest "run")))
)
