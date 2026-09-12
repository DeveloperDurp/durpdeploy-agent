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

## Development

Run the current agent source in the existing hardened Podman boundary:

```bash
make dev
```

This rebuilds the image once per invocation; it does not hot-reload source
changes. The default host port `10944` maps to the agent's container pairing
port `10943`, while the server's direct listener uses host port `10943`. If
the development port is occupied, choose another one explicitly instead of
falling back automatically:

```bash
AGENT_PORT=12044 make dev
```

The default persistent pairing state is the named Podman volume
`durpdeploy-agent-state`. Override it only with another named volume:

```bash
AGENT_STATE_VOLUME=durpdeploy-agent-dev make dev
```

Stopping the disposable agent container does not remove that volume. Keep the
paired state across compatible builds. Production execution must continue to
use the supplied systemd or hardened container definition.

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
