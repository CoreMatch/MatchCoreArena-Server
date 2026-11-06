# 0013. Observability Is an In-Process Counter Registry Exposed at /api/metrics

## Status
Accepted

## Context
Operating the reporting and matchmaking paths needs visibility: how many
matches settle, why reports get rejected, and how deep the queues are. No
external metrics infrastructure exists in this deployment.

## Decision
- `internal/metrics` keeps a dependency-free, mutex-guarded counter registry
  (`metrics.Default`). Counters are keyed by metric name with optional
  label suffixes, e.g. `matches_rejected_total:mca_report_inconsistent` or
  `matchmaking_groups_formed_total:2v2`.
- Instrumented points: match report success/replay and per-code rejection
  distribution (report handler), matchmaking joins/leaves, queue timeout
  removals, parties created, and per-mode group formation (engine).
- `GET /api/metrics` exposes a snapshot (`counters` map + live `queue_depth`
  per mode) and is restricted to OPS-level credentials like reporting
  (`403 oauth_insufficient_scope` for player tokens).
- Counters are process-local: they reset on restart and are not persisted or
  exported to any external system.

## Consequences
- The rejection-reason distribution is directly readable from the counters
  without log parsing; queue depth is computed live from MySQL.
- A future Prometheus-style exporter would wrap or replace the registry;
  key names are the stable surface until then.

## Evidence
- internal/metrics/metrics.go::Default — registry and counter key constants
- api/handler/metrics.go::Metrics — OPS-only snapshot endpoint
- api/router.go::Register — `/api/metrics` route
- api/handler/match.go::Report — success/replay/rejection counting
- internal/service/matchmaking.go::commitGroup — per-mode group formation counter
- test/e2e/e2e_test.go::TestMatchCoreE2E — metrics endpoint end-to-end assertions
