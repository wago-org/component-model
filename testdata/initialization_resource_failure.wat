;; The initializer acquires two ordinary host resources, then reports a
;; setup error. The runtime must destroy both resources before returning.
(component
  (import "test:initialization/resources" (instance $host
    (export "resource" (type (sub resource)))
    (export "acquire" (func (result (own 0))))
    (export "fail" (func))))
  (alias export $host "acquire" (func $acquire))
  (alias export $host "fail" (func $fail))
  (core func $acquire-core (canon lower (func $acquire)))
  (core func $fail-core (canon lower (func $fail)))
  (core instance $imports
    (export "acquire" (func $acquire-core))
    (export "fail" (func $fail-core)))
  (core module $initializer
    (import "host" "acquire" (func $acquire (result i32)))
    (import "host" "fail" (func $fail))
    (func $initialize
      call $acquire drop
      call $acquire drop
      call $fail)
    (start $initialize))
  (core instance $initialized
    (instantiate $initializer (with "host" (instance $imports)))))
