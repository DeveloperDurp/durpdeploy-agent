# DurpDeploy Agent

The repository builds the standalone DurpDeploy execution agent and the shared
Go packages used by the DurpDeploy control plane.

- Keep protocol changes backward compatible within `agent-v1`.
- Treat `protocol`, `transport`, `payload`, and `executor` as public packages.
- Keep pairing state compatible across upgrades.
- Run `make check` after Go changes and the container contract after image changes.
- Do not weaken TLS pinning, payload authentication, log redaction, or the Linux
  execution sandbox.
