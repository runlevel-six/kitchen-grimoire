# How to upgrade Kitchen Grimoire

Upgrades are a new container image. The server updates its own database
schema at startup, so there is no separate migration step.

## Images and versions

Each release tag `vX.Y.Z` publishes a multi-architecture image
(`linux/amd64`, `linux/arm64`) to `ghcr.io/runlevel-six/kitchen-grimoire`
with three tags:

| Tag | Moves? |
|---|---|
| `X.Y.Z`, e.g. `0.4.0` | Never. Pin this in production. |
| `X.Y`, e.g. `0.4` | To the newest patch release of that minor version. |
| `latest` | To the newest release. |

Note the image tags have no `v`. `grimoire version` (or
`grimoire_build_info` in the metrics) shows what is running.

Kitchen Grimoire is pre-1.0: read the release notes, or the changes to
`docs/DESIGN.md` §22 (Decision log) and `docs/status.md`, before upgrading
across minor versions.

## Upgrade on Kubernetes

1. **Take a backup** and check that it worked:

   ```sh
   kubectl -n recipes create job --from=cronjob/grimoire-backup grimoire-backup-pre-upgrade
   kubectl -n recipes logs -f job/grimoire-backup-pre-upgrade
   ```

2. Change the tag in your overlay's `kustomization.yaml`:

   ```yaml
   images:
     - name: ghcr.io/runlevel-six/kitchen-grimoire
       newTag: 0.4.0
   ```

3. Apply it:

   ```sh
   kubectl apply -k overlays/recipes
   kubectl -n recipes rollout status deployment/grimoire
   ```

   The Deployment uses the `Recreate` strategy: the old pod stops before
   the new one starts, because only one process may write the database.
   Expect a few seconds of downtime.

4. Check the log for the migrations it applied, and the schema:

   ```sh
   kubectl -n recipes logs deployment/grimoire | grep -E '"msg":"(starting|applied migration)"'
   kubectl -n recipes exec deployment/grimoire -- grimoire migrate --status
   ```

   Every migration should say `applied`.

The backup CronJob uses the same image name, so it is upgraded by the same
change.

## Upgrade with Docker Compose

```sh
docker compose run --rm grimoire-backup   # a backup first
# edit the image tag in compose.yaml
docker compose pull
docker compose up -d
docker compose logs grimoire | grep 'applied migration'
```

## What happens at startup

`grimoire serve` opens the database and applies, in order, every embedded
migration it hasn't applied yet, each in its own transaction, before it
takes traffic. `/readyz` doesn't answer `ok` until that's done. It
**refuses to start** (and logs why) if:

- the database was migrated by a **newer** version than this binary
  ("database was migrated by a newer version"): you are running an older
  image against newer data;
- an applied migration's file has **changed** since it was applied
  ("applied migration has changed"): a broken build. Don't edit applied
  migrations; report it.

You can list the state without changing anything:

```sh
grimoire migrate --status --data-dir /data
```

## Roll back

There are no down-migrations. If a new version applied a migration, the
older binary won't start against that database. To go back:

1. Scale the server to zero.
2. [Restore](backup-and-restore.md#restore-production) the backup you took
   before upgrading.
3. Deploy the older image tag.

Anything changed since that backup is lost, which is why step 1 of every
upgrade is a backup.

If the release added no migration (`migrate --status` lists the same
migrations before and after), you can simply deploy the older tag again.
