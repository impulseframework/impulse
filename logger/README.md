# logger

[![GoDoc](https://img.shields.io/badge/pkg.go.dev-doc-blue)](http://pkg.go.dev/github.com/impulseframework/impulse/logger)

**logger** is an HTTP request logger that implements correlated logging to one of several supported platforms. Each HTTP request is logged as the parent log, with all logs generated during the request as child logs.

The Logging destination is configured with an Exporter. This package provides Exporters for **Google Cloud Logging**, **AWS Logging**,
and **Console Logging**.

The _**GoogleCloudExporter**_ will also correlate logs to **Cloud Trace** if you instrument your code with tracing.

The _**ConsoleExporter**_ is useful for local development and debugging.

The _**AWSExporter**_ will also correlate logs to **AWS X-Ray** if you instrument your code with tracing and have logs sent to Cloudwatch. Note that additional configuration in the tracing is required to enable the correlation. In the tracing configuration, you must set the log group names to where the logs are being sent.

- Open telemetry documentation for [AWS logs](https://opentelemetry.io/docs/specs/otel/resource/semantic_conventions/cloud_provider/aws/logs/)
- X-Ray documentation for [log correlation](https://aws-otel.github.io/docs/getting-started/x-ray#using-config-to-set-cloud-watch-log-group-names)

## What a request writes: the policy

Each request, and each command-line run, carries a policy that decides at its end whether the parent entry is written. Lines the application logs during the request attach to it as child entries. The policy is one of four words:

| Word     | Constructor                | The parent entry is written when                                                                                                                                              |
| -------- | -------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| always   | `logger.Always()`          | the request ends, whatever happened. Today's behavior and the default of every exporter.                                                                                      |
| on event | `logger.OnEvent()`         | a line attached to the request, or the request failed: it answered 400 or above, or the run returned an error. A 500 raises the entry to error.                               |
| sampled  | `logger.Sampled(fraction)` | as on event, plus the declared fraction of the quiet requests, drawn once per request. The fraction must be above 0 and at most 1; anything else is refused with a panic.      |
| never    | `logger.Never()`           | not at all, whatever happened. A health check wants this.                                                                                                                     |

`MinSeverity` adds a floor to any word: `logger.OnEvent().MinSeverity(logging.Warning)` drops lines below Warning before they attach, so they neither count as an event nor appear as child entries.

The nearest declaration wins, from the outside in:

1. The exporter's own default: always, or on event for `GoogleCloudExporter.LogAll(false)` and `NewAWSExporter(false)`.
2. The request logger's options. `logger.DefaultPolicy(p)` sets the application default. `logger.PolicyByPrefix(map[string]logger.Policy{...})` sets the policy from the request path, longest prefix first, before the default; it is how routes an application mounts by hand get theirs. Both are options of `NewRequestLogger`; `NewCliLogger` takes `DefaultPolicy`.
3. `logger.WithPolicy(p)`, a middleware on a group of routes or on one route. Deeper setters override earlier ones.
4. `logger.FromCtx(ctx).SetPolicy(p)` inside a handler, which promotes or demotes its own request.

```go
r.Use(logger.NewRequestLogger(exporter,
	logger.DefaultPolicy(logger.Always()),
	logger.PolicyByPrefix(map[string]logger.Policy{"/files/": logger.OnEvent()}),
))
r.With(logger.WithPolicy(logger.Never())).Get("/healthz", health)
```

Every exporter honors the decision, the console included, so a local run shows the same behavior as the cloud. `Policy.String()` names a policy for a comment: `on event, Warning and above`.

### The equivalence with LogAll

`GoogleCloudExporter.LogAll(false)` and `NewAWSExporter(false)` keep working: they set the exporter's default policy to on event, where `LogAll(true)` and `NewAWSExporter(true)`, the defaults, set always. A `DefaultPolicy` option on the request logger overrides the exporter's default. One thing changes under on event: a request that answered 400 or above, or a run that returned an error, now writes its entry even when no line attached to it, where `LogAll(false)` used to drop it. A failure is an event.

## Switching the destination at run time

`logger.SwitchableExporter` is an `Exporter` whose destination can be replaced while the application runs: a device starts on the console, moves to the cloud once it can reach it, and comes back to the console when it loses that.

```go
exporter := logger.NewSwitchableExporter(nil) // nil means the console exporter
r.Use(logger.NewRequestLogger(exporter))

// once the cloud is reachable
previous := exporter.Swap(logger.NewGoogleCloudExporter(client, projectID))

// and when it is not any more
previous = exporter.Swap(logger.NewConsoleExporter())
```

A request or run goes to the exporter current when it starts and finishes on it even if a swap happens meanwhile. `Swap` is safe for concurrent use and returns the previous exporter so the caller can flush or close whatever stands behind it; the `Exporter` interface has no `Close`, so that is done on the concrete type the caller built.
