# How to operate Kitchen Grimoire

Routine operations for a running instance: logs, metrics, backups, the
trash, and the search index. The commands assume the Kubernetes setup from
[Deploy on Kubernetes](../tutorials/deploy-on-kubernetes.md), in the
namespace `recipes`; with Compose, use `docker compose logs` and
`docker compose exec` instead.

## Read the logs

The server logs JSON lines on standard error, one per request plus events
such as migrations, backups, imports, and trash purges:

```sh
kubectl -n recipes logs deployment/grimoire --since=1h
```

Each request line (`"msg":"request"`) has `id`, `method`, `route` (the
route pattern, not the raw path), `status`, `bytes`, `duration`, and
`user`. The same `id` is returned to the browser as the `X-Request-Id`
header, so a person can tell you which request failed.

Useful filters, with `jq`:

```sh
# failed requests
kubectl -n recipes logs deployment/grimoire | jq -c 'select(.msg=="request" and .status>=500)'
# requests refused for lack of identity, with the reason and peer
kubectl -n recipes logs deployment/grimoire | jq -c 'select(.msg=="unauthenticated request")'
# URL imports by site and outcome
kubectl -n recipes logs deployment/grimoire | jq -c 'select(.msg|startswith("url import")) | {domain, result}'
```

Recipe text and fetched pages are never logged. Set
`GRIMOIRE_LOG_LEVEL=debug` for more detail.

**`unauthenticated request` with `peer is not a trusted proxy`** means a
request reached the app from an address outside `GRIMOIRE_TRUSTED_PROXIES`.
If it's your proxy, the CIDR is wrong; if not, something is reaching the
pod that shouldn't (check the NetworkPolicy).

## Check health

| Endpoint (app port) | Meaning |
|---|---|
| `/healthz` | The process is up. Used by the liveness probe. |
| `/readyz` | The database answers within 2 seconds. Used by the readiness probe. |

```sh
kubectl -n recipes exec deployment/grimoire -- grimoire doctor /tmp
```

checks the SQLite features in the running container.

## Watch the metrics

`/metrics` is on the admin port (9090), which has no authentication. To
look by hand:

```sh
kubectl -n recipes port-forward deployment/grimoire 9090:9090
curl -s localhost:9090/metrics | grep -v '^#'
```

To scrape it with Prometheus, add an ingress rule to the NetworkPolicy in
your overlay that admits your Prometheus pods to port `admin`, and target
the `grimoire-admin` Service. Worth alerting on: backup staleness, 5xx
responses, and the ready state. Every metric and some example queries are
in [Metrics](../reference/metrics.md).

## Backups

The base includes a nightly CronJob (`grimoire-backup`, 03:27) that runs
`grimoire backup-pull` against the admin port and keeps the newest 14
backups on the `grimoire-backup` PVC.

```sh
# list backups
kubectl -n recipes get cronjob,jobs -l app.kubernetes.io/component=backup
# take one now
kubectl -n recipes create job --from=cronjob/grimoire-backup grimoire-backup-manual
kubectl -n recipes logs -f job/grimoire-backup-manual
```

The backup PVC lives on the same storage as the data, so also **copy
backups off the cluster**, and **practise a restore** every few months.
Both are in [Back up and restore](backup-and-restore.md).

## The trash

Deleted recipes stay in the trash for `GRIMOIRE_TRASH_DAYS` (default 30),
then are purged automatically: at startup and every 6 hours. Purging
deletes the recipe, its revisions, and its search entry. Photos no recipe
uses any more are deleted by a separate sweep every 6 hours (files younger
than an hour are kept). Both log a line when they remove something
(`purged trash`, `image GC removed unused files`).

To purge everything now, an admin uses **Trash → Empty trash** in the app.
There is no command-line equivalent.

## Rebuild the search index

The index is updated with every save and normally never needs attention.
If search results look wrong (say, after restoring a database by hand),
rebuild it:

```sh
kubectl -n recipes exec deployment/grimoire -- grimoire migrate --reindex
```

It reads every recipe, then rewrites the whole index in one transaction;
for a household library that takes a moment, and the server can keep
running.

## Restart, stop, and scale

```sh
kubectl -n recipes rollout restart deployment/grimoire
kubectl -n recipes scale deployment/grimoire --replicas=0   # stop (e.g. before a restore)
kubectl -n recipes scale deployment/grimoire --replicas=1
```

**Never run more than one replica.** SQLite has one writer, and the data
volume is ReadWriteOnce. The server finishes in-flight requests for up to
20 seconds on shutdown.

## Upgrade

See [Upgrade](upgrade.md).
