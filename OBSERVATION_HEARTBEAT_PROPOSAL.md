# Correlated radio-observation heartbeat proposal

Reference baseline: official WaveLogGate v2.1.1, commit `9344a4852ad461b60a50fbb384e2afdf17f6e414`.

Status: isolated reference candidate for upstream review. It is not an official release and must not be used to relax a production command gate until the protocol and all consumers are accepted and released.

## Problem statement

> Official WaveLogGate v2.1.1 does not continuously export successful unchanged radio polls. ShackCQ’s current 15-second dual-path pre-command policy therefore prevents reliable idle-radio control.

The v2.1.1 poller reads the configured controller every second, but exports only a changed state or a forced update after 30 minutes. `BroadcastStatus` timestamps and retains the bytes of a newly broadcast status; `Hub.add` sends those retained bytes unchanged to a new WebSocket client. The replay does not generate a replacement timestamp. The limitation is sparse successful-observation reporting plus the absence of explicit replay and cross-path correlation metadata.

The protocol must keep four facts distinct:

1. a successful radio observation;
2. replay of cached status;
3. gateway transport connectivity;
4. a failed radio poll or failed server delivery.

An old exported observation does not establish that the physical radio is disconnected. It establishes only that no recent successful observation has been exported.

## Capability negotiation and compatibility

The candidate discovers an additive, versioned capability through the existing authenticated v2 token-information request:

```json
{
  "extensions": {
    "shackcq.radio-observation.v1": {
      "version": 1,
      "report_interval_ms": 5000,
      "acknowledgement": "echo",
      "max_clock_skew_ms": 300000
    }
  }
}
```

The extension is enabled only when the exact name, version, acknowledgement mode and bounded timing values are present. A v2 key alone is not capability evidence. An absent extension keeps the existing 30-minute unchanged-state cadence. An unsupported or malformed offer fails closed. A profile, controller, credential or station change creates a new client session and disables the extension until it is negotiated again.

This preserves these combinations:

- unmodified client and existing server: unchanged;
- candidate client and legacy v1 server: legacy payload and cadence;
- candidate client and v2 server without the extension: legacy cadence and no extension fields;
- candidate client and extension-enabled ShackCQ endpoint: correlated heartbeat and exact acknowledgement.

## Successful observation identity

The controller remains single-owner and is polled once per second. A successful observation is exported on the first read, state change, recovery after failure, and the negotiated unchanged-state interval.

Each client session has a random `session_id`, a millisecond UTC `session_started_at`, and a sequence counter incremented for every controller poll attempt. Sequence values are positive JSON-safe integers and rotate into a new session before exceeding `9007199254740991`. Successful observations carry the same identity and source time over WSS and HTTPS:

```json
{
  "type": "radio_status",
  "frequency": 14042300,
  "mode": "USB",
  "timestamp": 1790995800123,
  "observation_protocol": "shackcq.radio-observation.v1",
  "session_id": "4c7701b6b7994e388c34d1c0bff455a9",
  "session_started_at": 1790995700123,
  "sequence": 42
}
```

`timestamp` is the successful controller-read time, truncated to UTC milliseconds. HTTP uses the equivalent fixed millisecond ISO-8601 form. Clock rollback within a session, source time more than five seconds ahead of browser receipt, and server source time outside the negotiated five-minute skew bound are rejected. Split operation normalizes the receive VFO and mode in the consumer; existing satellite and transverter offsets remain applied before export.

On WebSocket connection the hub may replay the retained observation with `cached:true`, preserving its original identity, source time and payload. Welcome frames, socket traffic and cached replay never create a successful observation.

## Credential-bound server path

The v2 radio request adds `source_protocol`, `source_session_id`, `source_session_started_at`, `source_sequence`, and `source_observed_at`. The ShackCQ compatibility endpoint binds the report to the authenticated credential, tenant, station profile and radio name. It stores one current-state row per credential and accepts only:

- a session with a later authenticated `session_started_at`; or
- a higher sequence in the current session.

An exact duplicate is idempotently acknowledged. A different payload under the same identity, a retired session, a lower sequence, an invalid time, or a mismatched binding is rejected. The response echoes the protocol, identity, times and a canonical SHA-256 payload hash. An unrelated 2xx, ignored metadata, authentication error, rate limit or server error is a delivery failure.

Opaque random session IDs provide correlation only. Current-session ownership comes from the credential-bound server transition ordered by `session_started_at`; the browser accepts that session only after the local live half matches the server’s current authenticated half. Once a replacement session is accepted, earlier sessions are retired and delayed evidence cannot reinstate them.

## Poll and delivery behavior

Poll and server-delivery results are transient evidence:

```json
{"type":"radio_poll_status","state":"poll_failed","session_id":"...","sequence":43,"timestamp":1790995801000,"failure_code":"RADIO_POLL_FAILED"}
{"type":"radio_delivery_status","state":"server_delivery_failed","session_id":"...","sequence":44,"timestamp":1790995802000,"observed_at":1790995801500,"failure_code":"SERVER_DELIVERY_FAILED"}
```

A failed poll has no successful observation time. A newer known poll or delivery failure invalidates command readiness until a later successful observation is accepted on both paths. Recovery at unchanged frequency and mode is a new observation.

HTTP delivery runs in one cancellable worker with a capacity-one latest-value queue. Slow or hung HTTP therefore cannot block one-second controller polling, create a goroutine per report or grow an unbounded queue. Superseded queued delivery is reported as failed. Shutdown cancels in-flight delivery and waits for both polling and delivery workers.

## Browser acceptance rules

The browser reducer binds credential, tenant station and radio profile, and tracks its local WSS connection generation. It accepts freshness only when a successful non-cached local observation and the credential-bound server observation have identical protocol, session, sequence, source time and normalized receive state.

It rejects cached frames, transport activity, acknowledgements, failures, reordered or conflicting duplicates, retired sessions, stale connection generations, wrong bindings, future time, clock rollback and values outside JSON-safe limits. A newer one-path observation or known failure clears older readiness. Pending halves expire after 60 seconds and are bounded to 64 entries. Browser reconnect clears authority from the previous connection even when retained frames or unmatched halves remain.

The two paths corroborate delivery and credential binding of one controller poll. They are not independent physical measurements.

## Cadence and capacity

The proposed five-second interval gives a 15-second consumer policy two missed report opportunities. It changes an opted-in connected client from about two unchanged reports per hour to 12 per minute. The isolated benchmark must report station count, accepted requests per second, PostgreSQL WAL bytes, process CPU/RSS, one-row current-state write behavior, pending-buffer bounds and outage recovery. Loopback synthetic throughput is not production capacity proof. A production rollout should add modest client jitter and validate the expected fleet against the deployed rate limiter and database.

## Separate policy decision

ShackCQ could separately choose an explicit receive-command-then-confirm policy: capture current state, send an owner-approved receive-only command, then require fresh matching readback and protect restoration from concurrent operator changes. That changes the safety model and manufactures activity to establish liveness. It is not implemented or approved by this proposal.

## Release boundary

This candidate changes no transmit, PTT, Tune, audio, power, antenna, rotator or QSO path. Passing isolated tests establishes a reviewable client/API/browser design. Official upstream acceptance and release, ShackCQ production migration and activation, and real operating acceptance remain separate decisions.
