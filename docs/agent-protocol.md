# Agent protocol

`agent/1`, `agent/2`, and opt-in `agent/3` are the outbound-only JSON contracts between
DurpDeploy and a remote agent. Version 2 adds explicit interpreter
capabilities. This document freezes the wire vocabulary only; it adds no
listener, database state, runner authorization, or fallback path.

## Endpoints and payloads

All JSON requests are exactly one object and require a present, non-null
protocol. They reject unknown fields, trailing JSON values, malformed JSON,
and unsupported protocol values. Pairing remains `agent/1`. Paired lifecycle
requests may use `agent/1`, `agent/2`, or `agent/3` during rollout.

| Endpoint | Request contract | Notes |
| --- | --- | --- |
| `POST /agent/v1/pairings/server-init` | `PairRequest` | Server-side pairing completion over mTLS. The first call uses `completion_ack: false`; after durable confirmation the same request is retried with `completion_ack: true` to perform listener cleanup.
| `POST /agent/v1/poll` | `PollRequest` | Protocol and agent version. Version 2 also requires `supported_interpreters`. A no-work response has no deployment payload. |
| `POST /agent/v1/deployments/{id}/start` | `StartRequest` | Acknowledges that the claimed work started. |
| `POST /agent/v1/deployments/{id}/heartbeat` | `HeartbeatRequest` | Response carries cancellation state and staged server fingerprints. |
| `POST /agent/v1/deployments/{id}/logs` | `LogBatchRequest` | Ordered line events. |
| `POST /agent/v1/deployments/{id}/result` | `ResultRequest` | Result state is only `succeeded` or `failed`. |
| `POST /agent/v1/deployments/{id}/cancelled` | `CancelledRequest` | Cancellation acknowledgement is distinct from a normal result. |

The endpoint route, active mTLS identity, and later persistence checks bind a
claim to one agent. This contract deliberately does not document credential
material, certificate bodies, or secret values.

## Bootstrap and pairing flow

The pairing flow is two-channel:

1. The unpaired local listener prints a short-lived pairing code.
2. The operator copies that code and the displayed agent fingerprint into the
   authenticated pairing form.
3. On confirmation, the server submits an mTLS request to
   `POST /agent/v1/pairings/server-init` with that code and fingerprint and
   `completion_ack: false`.
4. The listener verifies the request is over mTLS, validates the operator-
   confirmed identity values, and only then persists the server pin and pull
   endpoint.
5. The operator confirmation path uses the exact same identity, pin, and endpoint
   values for a second request with `completion_ack: true` when needed to recover a
   lost `204 No Content` response.
6. A listener may treat the second `completion_ack: true` request as idempotent and
   use it as the durable completion boundary for shutting down the temporary
   pairing callback.

Pairing code disclosure happens only on the local listener output; the code and
fingerprint are never surfaced through API responses.

## Bounds and timing

| Contract | Fixed value |
| --- | --- |
| Any request body | 1 MiB |
| Log batch | 100 events and 256 KiB |
| Log line | 16 KiB UTF-8 bytes |
| Poll interval / maximum long poll | 25 seconds |
| Heartbeat interval | 10 seconds |
| Pre-start claim expiry | 60 seconds |
| Started-work lost threshold | 45 seconds without heartbeat |
| Cancellation acknowledgement deadline | 30 seconds |

These are protocol constants, not configuration knobs. Oversize requests and
batches fail before later agent or persistence work consumes them.

## Direct assignment

An administrator explicitly assigns each remote environment to one paired
agent. Environments without an assignment execute locally. An assigned
environment creates work only for its paired agent; agents do not select work
or authorize themselves through labels.

## Dispatch state machine

| From | Allowed next state | Meaning |
| --- | --- | --- |
| `waiting` | `claimed` | A matching agent owns the pre-start claim. |
| `claimed` | `waiting` | Only the 60-second pre-start expiry can reclaim it. |
| `claimed` | `started`, `cancel_requested` | The agent starts, or cancellation overlays the claim. |
| `started` | `succeeded`, `failed`, `cancelled`, `lost`, `cancel_requested` | Started work reaches a terminal state, becomes lost after missed heartbeats, or receives cancellation. |
| `cancel_requested` | `cancelled`, `cancel_unconfirmed`, `lost` | The agent acknowledges cancellation, misses the 30-second acknowledgement deadline, or is lost. |

All other edges, including `started` to `waiting`, are invalid. A pre-start
claim may be reclaimed, but started work is never automatically replayed or
requeued and does not fall back to local execution; recovery is an explicit new
deployment.

## Transport trust

Agents initiate the connection. Both sides use self-signed X.509 identities
and pin the peer's SHA-256 certificate fingerprint directly. This has no
central CA, no trust-on-first-use mode, and no certificate-verification bypass.
Fingerprint rotation is staged over an already authenticated connection.

## Interpreter capabilities and payloads

The only interpreter values are `bash`, `pwsh`, and `python3`. An `agent/2`
poll reports a duplicate-free `supported_interpreters` array. The agent builds
that array with `exec.LookPath` inside its service or container boundary and
does not accept executable paths, command strings, or interpreter arguments.
An `agent/1` poll has no capability field and means Bash only.

Each immutable step may contain an `interpreter` field. A missing field means
Bash. Bash steps serialize without the field so strict v0.1.0 agents can decode
them during rollout. Non-Bash work is eligible only for an `agent/2` agent that
reported the selected value. The agent also checks the complete payload against
its discovered capabilities before acknowledging that a deployment started.

Each attempt writes `script.sh`, `script.ps1`, or `script.py` in private scratch
storage and invokes the resolved fixed executable directly with only the script
path as its argument. A missing executable fails the step without falling back
to Bash. The same environment filtering, process group, timeout, retry,
cancellation, log redaction, service identity, filesystem boundary, and cgroup
limits apply to every interpreter.

## Version 2 rollout

Upgrade every DurpDeploy server to a version that accepts both `agent/1` and
`agent/2` before starting v0.2.0 agents, because older servers reject the new
protocol and poll field. Keep accepting `agent/1` until all v0.1.0 agents are
retired. During the mixed-version window, omit the interpreter field for Bash
payloads and never dispatch non-Bash work to a v1 agent.

The agent's pairing state and `/agent/v1/...` endpoint paths do not change.
Protocol versions describe request contracts; they are independent of the
stable endpoint path namespace.

## Version 3 execution capabilities and rollout

Container execution is disabled by default. Enabling it selects `agent/3` for
polling and lifecycle requests. Pairing remains `agent/1`, and pairing state is
unchanged. An old server rejects v3 polling before issuing a claim; the agent
does not silently downgrade to an incompatible protocol.

A v3 poll contains all four non-null capability arrays:

```json
{
  "protocol": "agent/3",
  "agent_version": "v0.3.0",
  "supported_interpreters": ["bash"],
  "execution_modes": ["host", "container"],
  "container_runtimes": ["podman"],
  "container_interpreters": ["bash", "pwsh", "python3"]
}
```

`supported_interpreters` describes executables installed in the host/service
boundary. `container_interpreters` describes fixed entrypoints supported by the
runner; the selected image must supply that executable. These lists are separate
so a Bash-only agent image does not advertise host PowerShell or Python support.
Modes are only `host` and `container`; runtimes are only `docker` and `podman`.
Unknown values, duplicates, omitted/null arrays, and contradictory combinations
are rejected. A host mode requires host interpreters; container mode requires
both a ready runtime and container interpreters. A container-only agent reports
an empty host interpreter array and omits `host` from its modes.

Runtime preflight requires an accessible Unix socket, Linux, seccomp, and CPU,
memory and PID cgroup support. Container steps request no per-step RAM ceiling
or CPU quota, including retries; applicable external limits still govern them.
They retain the 128-process limit, read-only root, bounded 64 MiB tmpfs, non-root
user, network isolation, dropped capabilities, and seccomp policy. Operators must
provide external resource policy because a step can consume more host RAM and CPU.
Agent service and host-mode limits are unchanged.
Runtime preflight reconciles containers labelled with the paired
agent identity namespace before every poll attempt, including transport retries.
No poll is sent while runtime access or cleanup is uncertain. The agent validates
the complete claimed payload and rechecks readiness before start.

V3 immutable steps add `execution_mode`, `container_image`, and `variable_names`.
Missing mode means host. Host steps reject images; container steps require an
image. Empty variable selection passes all compatible resolved variables; a
non-empty selection passes only named variables. Missing selected variables,
duplicate names, or reserved container selections fail before running the step.
Container defaults exclude runtime-client configuration variables. Secret values
are passed through environment entries, never command arguments, and all payload
secrets remain in the log scrubber even when a step does not select them.
Output lines are limited to 1 MiB. Exceeding this limit stops execution without
retry and confirms container removal before reporting failure.
The agent frames already-redacted output within the existing per-event and
encoded-batch byte limits, preserving UTF-8 boundaries.

Servers must use `executor.Step.MarshalForProtocol` (or an equivalent validated
payload boundary) after capability-aware selection. It omits every v3-only field
for compatible v1/v2 host work and refuses container or restricted-variable work
for legacy protocols. V1 also refuses non-Bash work. Do not remove fields from
incompatible work and dispatch it as a host step.

The v3-only result state `cleanup_unconfirmed` is terminal and cannot be replayed.
It takes precedence over success, failure, and cancellation acknowledgement when
container removal cannot be confirmed. The agent retains its non-secret claim
marker and stops polling until runtime reconciliation succeeds. The result's
`error` contains a fixed operator action, without workload output or secrets.
Coordinated server support must persist this state, block overlapping work, and
use subsequent authenticated ready polling to confirm reconciliation.

Upgrade all servers with the shared v3 contract, dispatch filtering, encrypted
payload generation, and cleanup-state handling before releasing or enabling v3
agents. Server implementation is tracked in DurpDeploy #99. Keep v1/v2 host
support during mixed-fleet rollout. Disable container mode and remove socket
access only after in-flight attempts and cleanup uncertainty are resolved;
container work remains ineligible for downgraded agents.
