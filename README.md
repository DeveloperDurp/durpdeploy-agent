# DurpDeploy Agent

The DurpDeploy Agent executes deployment steps on a remote Linux host. It pairs
with a DurpDeploy control plane over HTTPS, pins the server identity, decrypts
authenticated deployment payloads, streams redacted logs, and runs Bash steps
under the preselected unprivileged agent identity inside a hardened service or
container boundary. The agent and Bash have zero Linux capabilities.

## Build

Requires Go 1.25.7 or newer.

```sh
make build
make check
```

## Run

Create the service account described in
[`docs/agents.md`](docs/agents.md), then start `durpdeploy-agent` through the
provided systemd or container contract. On first start the process prints its
pairing code and certificate fingerprint.

Configuration remains compatible with existing installations:

- `DURPDEPLOY_AGENT_LISTEN_ADDR`
- `DURPDEPLOY_AGENT_STATE_DIR`
- `DURPDEPLOY_AGENT_VERSION`
- `DURPDEPLOY_EXTRA_SCRUB_PATTERNS`

The state directory contains the persistent agent identity and server pins. Do
not delete it during an upgrade.

The agent and scripts share one UID because switching to a separate runner UID
would require the forbidden `SETUID` and `SETGID` capabilities. Scripts can
therefore read or change agent state writable by that UID. Operators are
responsible for scripts, supplied secrets, network access, and all effects
inside the boundary. Use one separately hosted agent per trusted script domain.

## Shared packages

DurpDeploy pins this module and uses `protocol`, `transport`, `payload`, and
`executor`. Breaking wire changes require a new protocol identifier; `agent-v1`
changes must remain backward compatible.
