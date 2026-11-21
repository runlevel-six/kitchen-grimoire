# Metrics

`GET /metrics` on the **admin port** (`GRIMOIRE_ADMIN_LISTEN`, default
`:9090`) serves Prometheus metrics in the text exposition format
(`text/plain; version=0.0.4`). The admin port has no authentication, so
scrape it from inside the cluster and let the NetworkPolicy admit your
scraper (the base policy admits only the backup job).

Metrics are kept in memory and start from zero when the server restarts.

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `grimoire_http_requests_total` | counter | `route`, `code` | Requests served on the app port. `route` is the matched route pattern, such as `GET /r/{slug}`, or `unmatched`; `code` is the HTTP status. |
| `grimoire_http_duration_seconds` | histogram | `route` | App-port request latency in seconds. Buckets: 5 ms, 10 ms, 25 ms, 50 ms, 100 ms, 250 ms, 500 ms, 1 s, 2.5 s, 5 s, 10 s. |
| `grimoire_scrape_total` | counter | `result` | URL import attempts by outcome: `jsonld`, `microdata`, `opengraph_stub` (no recipe data, a stub from the page title), `blocked` (the site answered 401, 403, 429, or 503), or `error` (anything else, including pages with no recipe). |
| `grimoire_recipes` | gauge | none | Recipes in the library, not counting the trash. Read from the database at scrape time; missing from the output if the query fails. |
| `grimoire_backup_last_success_timestamp` | gauge | none | Unix time of the last backup streamed completely from `/admin/backup`. Absent until the first backup since the server started. |
| `grimoire_build_info` | gauge | `version`, `commit`, `go` | Always `1`; the labels identify the build. |

Labels never contain raw paths, recipe names, or domains. Which site an
import came from is in the logs (`url import` lines, field `domain`), not
in metrics.

Admin-port requests themselves (`/metrics`, `/admin/backup`) are logged but
not counted in the HTTP metrics.

## Useful queries

Backups are stale (no backup for 36 hours, or none since a restart a day
ago):

```promql
time() - grimoire_backup_last_success_timestamp > 36 * 3600
```

```promql
absent_over_time(grimoire_backup_last_success_timestamp[1d])
```

Share of URL imports blocked by the site, over a week:

```promql
sum(increase(grimoire_scrape_total{result="blocked"}[7d]))
  / sum(increase(grimoire_scrape_total[7d]))
```

Server errors:

```promql
sum by (route) (rate(grimoire_http_requests_total{code=~"5.."}[5m]))
```

95th-percentile page time:

```promql
histogram_quantile(0.95, sum by (le) (rate(grimoire_http_duration_seconds_bucket[5m])))
```
