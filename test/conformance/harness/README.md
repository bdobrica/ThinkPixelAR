# HarnessAdapter first-turn conformance

This shared suite checks the initial demo path through the real Go HarnessAdapter
port, independently of Codex. It is test support and is never registered as a
production adapter. It builds on ADRs 0044–0045 without changing their contracts.

Run the framework's self-tests:

```sh
go test -race -timeout 2m ./test/conformance/harness
```

A new adapter test calls:

```go
func TestAdapterConformance(t *testing.T) {
    harnessconformance.Run(t, func(t *testing.T) harnessconformance.Fixture {
        // Construct a fresh adapter and trusted bound requests for this case.
        // Register unconditional process/transport cleanup with t.Cleanup.
        return newAdapterFixture(t)
    })
}
```

Import `github.com/bdobrica/ThinkPixelAR/test/conformance/harness` as
`harnessconformance`. The factory supplies the expected immutable negotiation
result, start/execute/interrupt/close requests, distinct operation identities and
valid digests. Select a deterministic workload that yields at least two events
and remains interruptible. Cases are serial and each receives a fresh adapter;
they neither share a running turn nor require an external service by themselves.
For a packaged adapter, the factory owns its process and transport setup.

The five cases cover:

- Deterministic descriptor, demo capabilities and pinned registry selection.
- Wrong kind/contract/required capability rejection and exact positive negotiation.
- Start readiness, event identity/fence/operation/sequence/size/classification,
  cancelled reads, interrupt, close and repeated close.
- Start replay/conflict, stale-generation handle rejection and execution replay
  retaining its operation stream; changed request digests conflict.
- Stable refusal of unadvertised resume, signal and checkpoint operations,
  without changing observed readiness. This does not qualify advertised optional
  capabilities; each still needs its own positive tests.

Each case uses a 30-second context, which implementations must honor. `go test
-timeout` is the outer watchdog for broken adapters that ignore cancellation.
Best-effort adapter close uses a separate five-second context; the factory's
cleanup remains mandatory even if startup returns a malformed handle. Error
messages identify checks only, never vendor payloads or raw diagnostics.

The self-test adapter is an in-memory double, with deliberate defects verifying
that the checks fail. It emits registered `execution.progress` candidates under
the HNS-004 schema; these are not committed Runtime Events. Declaration checks
reject unknown types and unnegotiated event capabilities. It does not replace AGD-017's real process fixture
or AGD-020's authenticated integration test.

This baseline is a framework, not complete adapter qualification. HNS-004 defines
canonical mapping rules; CDX adds the actual protocol driver and runs this suite
against its packaged harness. Handshake substitution, crash/timeout ambiguity,
all-mutation races, slow-consumer overflow, reasoning/secret exclusion and positive
optional-feature tests remain required for their corresponding implementation
claims. A passing self-test is not evidence of a real Codex turn or Kata isolation.
