# Configuration model

The `configuration` package provides the startup configuration loader.
The `resourcepolicy` package provides the shared resource model and its validator.
The executable resolves startup settings before pairing, polling, or execution.
It captures those values for the lifetime of the process.
Executable startup accepts validated custom resource values and carries the captured snapshot across reconnects.
Custom policies require a matching acknowledgement before the client can return or execute claimed work.
Default policies retain support for legacy servers without acknowledgements.

## Go interface

Call `configuration.Load()` to resolve the current environment.
Call `configuration.LoadWithLookup(lookup)` to use an isolated environment source.
Use the returned value as a startup snapshot.
Before starting services, apply existing consumer validators to legacy textual settings.
The loader validates resource values before returning a snapshot.

Values use this precedence: explicit environment variable, optional complete YAML, then built-in default.
Set `DURPDEPLOY_AGENT_CONFIG_FILE` to select the YAML file for the loader.
Leave the selector unset or empty to use environment variables only.
Resolve relative paths from the process working directory.
Use [the complete example](../examples/agent-config.yaml) for the field names and resource defaults.

Restart the agent after changing its environment or selected YAML file.
Help and version commands remain independent of the configuration file and runtime validation.
The internal host supervisor also bypasses application startup, as required for cleanup.

An explicitly empty environment value replaces the YAML value.
An empty numeric value fails validation.
An empty legacy text value retains its existing consumer meaning.
The loader does not expand environment references inside YAML.
It does not modify the process environment, create state, contact services, or read pairing identities.

The loader rejects unreadable selected files, unknown keys, duplicate keys, invalid scalar types, aliases, anchors, and multiple documents.
It also rejects null values, integer overflow, non-finite CPU values, duration overflow, and unsafe capacity arithmetic.
Errors omit input values and selected file paths.
Store operator configuration with private file permissions.

Use `executor.NewExecutorWithSettings` to capture execution settings for library callers.
Use `agentclient.NewPairedWithSettings` inside the agent to carry that snapshot across claims.
Existing default constructors remain available for callers that use environment settings.
The explicit scrubber constructor returns a redacted error for invalid patterns.
The legacy `NewScrubber` signature masks all output when its pattern configuration is invalid.

The executable rejects invalid extra scrub patterns before it creates state or opens a listener.
It retains the existing comma separator for extra patterns.
Do not include commas inside one pattern.
The captured execution boundary still requires `service` for Linux host jobs.

Use `executor.NewExecutorWithExecutionPolicy` to supply an explicit retry and timeout policy through the Go interface.
Use `agentclient.NewPairedWithExecutionPolicy` to capture that policy with a paired client.
The client and executor copy the policy. Later caller changes do not affect it.

Default constructors use 10 retries, a 86400-second maximum, and a 300-second default timeout.
Zero retries means one attempt. Zero timeout uses the captured default.
Positive fractional durations remain supported by direct Go jobs. Negative durations fail validation.

Payload preflight, step support, direct jobs, and ordered snapshots use the same execution limits.
An invalid later step prevents all earlier steps from starting.
Snapshot validation occurs before duration conversion and retry narrowing.
The executor retains bounded retries at the largest supported native integer count.
Policy errors use `resourcepolicy.ErrExecutionLimits` for invalid step requests.

Paired clients advertise their captured policy through poll headers.

Use `executor.NewContainerExecutorWithPolicy` to capture container resource values through the Go interface.
Use `agentclient.EnableContainersWithPolicy` inside the agent to apply those values before polling.
Both interfaces validate policy before runtime effects. Later caller or environment changes do not change captured values.

Use `executor.NewContainerExecutorWithResourcePolicy` to capture the complete resource snapshot.
Paired clients pass their captured snapshot when they enable containers.
The explicit container override replaces only the container group.

Workload attempts use the captured PID budget and temporary filesystem size, including retries.
Writable staging and read-only artifact helpers use the captured PID, CPU, and memory overhead values.
Writable volume capacity equals staging payload bytes plus one memory page per staging entry.
Read-only artifact capacity equals extracted bytes plus one memory page per artifact staging node.
Each volume allows twice its node count plus two inodes, including the root node.
Helper memory equals its volume capacity plus the configured memory overhead.
The artifact default uses 19999 nodes. The staging default uses 20000 entries.
The derived default artifact capacity is one memory page smaller than the former shared staging capacity.
Its 512 MiB extracted payload budget remains unchanged.
Runtime flags retain their existing controller requirements. This change adds no cgroup delegation requirement for staging transfer.

Default constructors retain 128 workload PIDs, 64 MiB temporary storage, and 32 helper PIDs.
Helpers retain one CPU and 128 MiB memory overhead by default.
Higher PID and storage budgets permit more runtime demand. Lower values can cause workloads or helpers to fail.
Workload containers receive no new CPU or memory quota. Host jobs receive no container PID budget.

Use `artifact.Pin.ValidateWithPolicy` to check a pin against an explicit artifact policy.
Use `artifact.ValidateZIPWithPolicy` and `artifact.ExtractZIPWithPolicy` to apply that policy to a package.
Start with `resourcepolicy.Defaults().Artifact`, then replace the required fields.
Each call validates and copies its policy before parser reads or extraction effects.

These interfaces enforce download bytes, extracted bytes, ZIP entry count, staging nodes, and cumulative metadata reads.
Staging nodes include implicit parent directories. Shared parents count once.
Metadata reads include parser searches and repeated reads. Payload reads do not consume the metadata budget after parsing.
Repeated explicit directory entries remain invalid. Existing path, file-type, cancellation, and private-tree cleanup checks remain enforced.

Default wrappers and exported constants retain their existing budgets.
Metadata-limit errors retain `artifact.ErrMetadataLimit` and `artifact.ErrInvalid` classification, with generic text for custom budgets.
Filesystem path and component bounds remain fixed platform constraints.
Higher budgets permit more disk, memory, parser, and extraction work. Lower budgets can reject existing packages.

Use `agentclient.NewPairedWithResourcePolicy` to capture the complete validated resource snapshot before polling.
The client copies that snapshot. Later caller or environment changes do not change it.
Use `client.ResourcePolicy()` to read a value copy.
Existing paired-client constructors retain their current default policies.

Claim preflight and artifact downloads use the captured download budget before issuing package requests.
Claim extraction uses the captured extraction, entry, node, and metadata budgets.
Rejected downloads and extraction failures retain their existing private-file cleanup.
Startup carries the complete snapshot across reconnects.

Artifact downloads use the captured fetch, dial, TLS handshake, and response-header timeouts.
The fetch timeout covers all attempts and backoff. An earlier caller deadline still applies.
Each fetch uses a separate pinned transport. It closes idle connections after completion or failure.
The control-plane transport retains its existing settings.

Artifact downloads and staging uploads/downloads use the captured transfer idle timeout.
Transferred bytes reset the idle window. A stalled request cancels after that window.
Staging transfers retain their caller deadline without an additional total fetch timeout.
The default artifact fetch budget is 300 seconds, including retries.
Default dial, TLS handshake, response-header, and transfer idle budgets are 30, 10, 30, and 300 seconds.

Staging transfers use the captured archive-byte budget for download headers, copied bytes, and upload sizes.
Downloaded archives also use the captured payload and entry/node budgets.
Container helpers use captured staging and artifact capacities through the full-resource constructor.
Configure container resources through the explicit container interface.

Use `staging.PackWithPolicy` and `staging.UnpackWithPolicy` inside the agent to supply explicit staging archive budgets.
These internal Go interfaces validate and copy `resourcepolicy.Staging` before archive I/O or filesystem effects.
They apply payload bytes, entries, and complete TAR bytes, including framing and trailing padding.
Entry limits include implicit parent directories during unpacking.
Packing counts each file and directory in the source tree.

Existing `Pack` and `Unpack` wrappers retain the default budgets and error classification.
Packing now rejects excessive archive output before exceeding its byte budget.
Unpacking also rejects excessive trailing zero padding.
Discard partial packed output after an error. Clean the destination after an unpacking error.
The caller retains ownership of those cleanup operations.
Path, type, duplicate-entry, private-mode, cancellation, and parser framing checks remain enforced.

Claimed host workspaces use the same captured staging budgets for import, output packing, and publication validation.
Container output also passes captured archive validation before publication.
Container exports use the captured staging archive-byte budget with one overflow byte for rejection.
Discard partial export output after any error.

## Policy compatibility Go interface

Call `limits.Advertisement()` to identify a validated complete resource snapshot.
Call `limits.CheckCompatibility(advertisement)` before dispatch to check a peer against the required snapshot.
The executable uses the captured policy through these interfaces before it polls for work.

Poll requests use `resourcepolicy.CapabilityHeader`, `X-DurpDeploy-Resource-Policy`, for the advertisement.
Require one header value. Reject repeated header values before calling the compatibility check.

Version 1 uses `resource-policy/1;sha256=` followed by 64 lowercase hexadecimal characters.
Hash the compact `encoding/json` representation of `resourcepolicy.Limits` with SHA-256.
Use its declared field order, JSON field names, and Go numeric encoding without trailing whitespace.
Change the advertisement version when that representation or its enforcement semantics change.

This digest is a compatibility identifier. It does not authenticate a peer.
Keep the existing mutual TLS authentication and certificate checks.

Policy-aware peers must report exactly the same snapshot, including timeouts and container limits.
An absent advertisement permits only the default policy used by legacy agents.
Custom policies require explicit matching support. The check does not clamp values or silently accept different ceilings.
Invalid, unknown, and mismatched advertisements return `resourcepolicy.ErrIncompatible` without retaining the untrusted value.
Invalid local policies fail validation before advertisement or compatibility checks.

The client validates successful 200 and 204 poll acknowledgements before decoding or returning a claim.
Custom policies require exactly one matching acknowledgement. Default policies also accept an absent legacy acknowledgement.
Repeated, explicitly empty, malformed, unknown, and mismatched acknowledgements fail with a typed incompatibility error.
Other status handling, retry backoff, certificate checks, and lifecycle requests retain their existing behavior.

Configure containers before polling. A successful explicit container override also updates the client's captured effective policy.
Failed container initialization does not change that policy.
Later caller or environment changes do not change the captured snapshot.

Coordinated server acknowledgement, dispatch enforcement, and dependency adoption remain pending.
An unsupported server can claim work before a custom SDK client rejects its missing acknowledgement.
The client does not return or execute that claim. The server must enforce compatibility before dispatch.
Request bodies and encrypted payloads retain their existing strict schemas.

## Startup inputs

| YAML field | Environment variable | Empty-value meaning in the existing consumer |
| --- | --- | --- |
| `state_dir` | `DURPDEPLOY_AGENT_STATE_DIR` | Use the platform configuration directory plus `durpdeploy-agent`. |
| `listen_addr` | `DURPDEPLOY_AGENT_LISTEN_ADDR` | Use the pairing listener default. |
| `version` | `DURPDEPLOY_AGENT_VERSION` | Report the executable build identity. |
| `hostname` | `DURPDEPLOY_AGENT_HOSTNAME` | Detect the runtime or local hostname. |
| `execution_boundary` | `DURPDEPLOY_AGENT_EXECUTION_BOUNDARY` | Linux host execution requires the service boundary. |
| `extra_scrub_patterns` | `DURPDEPLOY_EXTRA_SCRUB_PATTERNS` | Use the built-in scrub patterns. |
| `container.enabled` | `DURPDEPLOY_AGENT_CONTAINER_ENABLED` | Disable container execution. |
| `container.runtime` | `DURPDEPLOY_AGENT_CONTAINER_RUNTIME` | Container execution requires Docker or Podman. |
| `container.socket` | `DURPDEPLOY_AGENT_CONTAINER_SOCKET` | Container execution requires an absolute local Unix socket URL. |

Pairing supplies the server URL, certificate pins, and agent identity.
These values remain in pairing state. They are not startup configuration fields.
Do not provide manual server connection settings.

`LANG`, proxy variables, certificate variables, `TMPDIR`, and platform configuration directories retain their upstream meanings.
Container detection markers are platform evidence rather than application settings.
Build metadata inputs remain build settings.
Deployment variables remain authenticated job data.

## Resource settings

Derive each resource environment name from its YAML path.
Replace dots with underscores and convert letters to uppercase.
Add the prefix `DURPDEPLOY_AGENT_`.
For example, `limits.container.pids` maps to `DURPDEPLOY_AGENT_LIMITS_CONTAINER_PIDS`.

| YAML group | Fields | Units |
| --- | --- | --- |
| `limits.execution` | `max_retries`, `max_timeout_seconds`, `default_timeout_seconds` | Retries and seconds. |
| `limits.artifact` | `download_bytes`, `extracted_bytes`, `files`, `staging_nodes`, `metadata_bytes` | Bytes and entry counts. |
| `limits.artifact` | `fetch_timeout_seconds`, `dial_timeout_seconds`, `tls_timeout_seconds`, `header_timeout_seconds` | Seconds. |
| `limits.staging` | `payload_bytes`, `entries`, `archive_bytes` | Bytes and entry counts. |
| `limits.transfer` | `idle_timeout_seconds` | Seconds. |
| `limits.container` | `pids`, `helper_pids` | Processes and threads per container. |
| `limits.container` | `tmpfs_bytes`, `helper_memory_overhead_bytes` | Bytes. |
| `limits.container` | `helper_cpus` | CPU cores. |

Retries can be zero. Other integer resource values must be positive.
The default timeout cannot exceed the maximum timeout.
Artifact staging nodes must cover the artifact file count.
The staging archive budget must include payload bytes and TAR framing.
All values must fit their consumer arithmetic, including sentinel bytes, duration conversion, inode capacity, and helper memory.
Helper CPUs must fit the runtime nanosecond conversion and remain at least `0.000000001`.

Container PID controllers count processes and threads together.
Host-mode execution does not receive a container PID budget.
The service boundary and cleanup guarantees remain mandatory.
Configure the same shared resource snapshot on the policy-aware server and agent.
Restart the agent after changing its configuration.

Use environment variables for an environment-only deployment:

```sh
DURPDEPLOY_AGENT_LIMITS_EXECUTION_MAX_RETRIES=5 \
DURPDEPLOY_AGENT_LIMITS_CONTAINER_PIDS=64 \
durpdeploy-agent
```

Pair this agent with a policy-aware server configured with the same shared snapshot.
Use `DURPDEPLOY_AGENT_CONFIG_FILE=/private/agent.yaml` to load a complete YAML file instead.
Environment variables continue to override individual YAML settings.
Invalid values fail before state acquisition, pairing, polling, or runtime initialization.
An unpaired agent can accept valid custom configuration before a compatible server pairs it.

The agent model excludes server-only HTTP, merge, transfer-admission, token-telemetry, and log-preview budgets.
Protocol framing, heartbeat intervals, pairing expiry, and certificate limits remain protocol boundaries.
Poll headers advertise the complete shared resource snapshot without changing request bodies.
Existing servers retain their current dispatch behavior until coordinated server support is adopted.

## Dependency license

The loader uses `go.yaml.in/yaml/v3` version `v3.0.5`.
Its source uses MIT and Apache-2.0 licenses and declares no module dependencies.
The upstream module includes `LICENSE` and `NOTICE` files.
Required notices are retained in [third_party/go-yaml](../third_party/go-yaml/).
