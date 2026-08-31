# ENG-006 dependency review

Date: 2026-08-31

ENG-006 adds two focused external runtime-library families owned by
`internal/telemetry`:

| Dependency | Version | Source | License | Purpose and boundary |
| --- | --- | --- | --- | --- |
| `github.com/prometheus/client_golang` | `v1.24.1` | Prometheus project / Go module proxy checksum database | Apache-2.0 | Prometheus collector registry and text exposition. The package is confined to telemetry and does not define domain or authority types. |
| `go.opentelemetry.io/otel` and `go.opentelemetry.io/otel/sdk` | `v1.46.0` | OpenTelemetry project / Go module proxy checksum database | Apache-2.0 | Vendor-neutral trace API/SDK initialization. Exporters are injected and no provider transport enters the domain boundary. |

The standard library has no Prometheus exposition or OpenTelemetry protocol
implementation. These established vendor-neutral libraries avoid a custom wire
format and keep exporters replaceable. The selected versions were the latest
stable releases reported by the Go module proxy on the review date. Transitive
modules are pinned by `go.mod`/`go.sum`; their inventory, source, license, native
code, and vulnerability checks remain subject to the automated ENG-011 gate.

Neither library is an authorization boundary. The registry exposes only
predeclared bounded labels, and trace initialization records only validated
service metadata unless later reviewed instrumentation explicitly adds safe
attributes.
