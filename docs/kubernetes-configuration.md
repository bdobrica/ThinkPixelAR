# Kubernetes control-plane connection

The infrastructure adapter uses `config.LoadKubernetes` and
`kubernetes.Connect`. This is a library seam; the HTTP-only service does not yet
construct a sandbox provider at startup.

| Environment variable | Meaning |
| --- | --- |
| `THINKPIXELAR_KUBERNETES_MODE` | `in-cluster` (default) or `development` |
| `THINKPIXELAR_KUBERNETES_NAMESPACE` | Required single namespace |
| `THINKPIXELAR_KUBERNETES_KUBECONFIG` | Required explicit file in development; forbidden in-cluster |
| `THINKPIXELAR_KUBERNETES_CONTEXT` | Optional explicit context in development; forbidden in-cluster |
| `THINKPIXELAR_KUBERNETES_API_TIMEOUT` | Go duration; default `15s`, positive and at most `1m` |

In-cluster mode uses the AR control-plane ServiceAccount, never a sandbox
ServiceAccount. Development files are trusted operator input and may invoke
credential plugins. Neither mode falls back to another credential source.
TLS verification is required. No credentials should be committed or printed.

The official client is pinned to `v0.36.2`. Requests are rate limited to 10 QPS
with burst 20 and bounded by an HTTP timeout and caller context. Cluster RBAC
must scope the caller to its intended resources; the namespace configuration
alone is not authorization. Native API errors are translated by consuming
adapters before crossing the neutral provider boundary.
