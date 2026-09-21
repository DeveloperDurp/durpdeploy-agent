# Agent protocol

`agent/1` and `agent/2` are the outbound-only JSON contracts between
DurpDeploy and a remote agent. Version 2 adds explicit interpreter
capabilities. This document freezes the wire vocabulary only; it adds no
listener, database state, runner authorization, or fallback path.

## Endpoints and payloads

All JSON requests are exactly one object and require a present, non-null
protocol. They reject unknown fields, trailing JSON values, malformed JSON,
and unsupported protocol values. Pairing remains `agent/1`. Paired lifecycle
requests may use `agent/1` or `agent/2` during rollout.

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
