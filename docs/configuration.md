# Process configuration

ThinkPixelAR loads trusted operator configuration through the internal typed
configuration boundary. Configuration never comes from Workspace content,
agent images, harness state, model output, or other sandbox-controlled input.

## Loading and precedence

Configuration is applied in this order, with later sources taking precedence:

1. conservative built-in defaults;
2. an optional JSON file selected by `THINKPIXELAR_CONFIG_FILE` or an explicit
   process bootstrap option; and
3. individual environment variables.

JSON files are limited to 1 MiB and decoded strictly. Unknown fields, malformed
values, multiple JSON values, invalid or oversized addresses and PostgreSQL
URLs, non-positive or excessive timeouts, and unknown deployment environments
fail startup. A failed load returns no partially validated configuration.

The initial file shape is:

```json
{
  "environment": "production",
  "http": {
    "listen_address": "0.0.0.0:8080",
    "read_header_timeout": "5s",
    "read_timeout": "30s",
    "write_timeout": "30s",
    "idle_timeout": "2m",
    "shutdown_timeout": "30s"
  },
  "database": {
    "url": "postgres://user:password@database.example/thinkpixelar"
  }
}
```

The example credential is illustrative only. Do not commit real credentials.
Prefer injecting `THINKPIXELAR_DATABASE_URL` through the deployment's approved
secret mechanism instead of placing it in a file.

## Environment variables

| Variable | Meaning |
| --- | --- |
| `THINKPIXELAR_CONFIG_FILE` | Optional JSON configuration path. |
| `THINKPIXELAR_ENVIRONMENT` | `development`, `test`, or `production`. |
| `THINKPIXELAR_HTTP_LISTEN_ADDRESS` | HTTP `host:port` listener. |
| `THINKPIXELAR_HTTP_READ_HEADER_TIMEOUT` | Positive Go duration, at most 10 minutes. |
| `THINKPIXELAR_HTTP_READ_TIMEOUT` | Positive Go duration, at most 10 minutes. |
| `THINKPIXELAR_HTTP_WRITE_TIMEOUT` | Positive Go duration, at most 10 minutes. |
| `THINKPIXELAR_HTTP_IDLE_TIMEOUT` | Positive Go duration, at most 10 minutes. |
| `THINKPIXELAR_HTTP_SHUTDOWN_TIMEOUT` | Positive Go duration, at most 10 minutes. |
| `THINKPIXELAR_DATABASE_URL` | PostgreSQL URL; treated as `Restricted`. |

## Safe defaults and production validation

The default environment is `development`, the listener is loopback-only at
`127.0.0.1:8080`, and no database credential is synthesized. Production mode
rejects loopback listeners and requires a PostgreSQL URL with a host and
database name. Environment variables can override file values, including with
an explicitly empty value; validation then decides whether the result is
acceptable.

The database URL uses a secret wrapper whose text, formatted, and JSON forms
emit `[REDACTED]`. Diagnostic boundaries can also remove the exact loaded URL
and its embedded password. Code must call `Reveal` only at the narrow adapter
boundary that opens the database connection. Whole configuration objects and
process environments must not be logged. This supports, but does not replace,
the recursive logging redaction required by ENG-005 and the normative
[data-classification contract](security/data-classification.md).
