# How to back up and restore Kitchen Grimoire

This guide shows how to take scheduled backups, check that a backup is
good, practise a restore, and copy backups off the cluster. The design
behind it is in [DESIGN.md §14.4](../DESIGN.md#144-backup-and-restore).

A backup is one `grimoire-YYYYMMDDTHHMMSSZ.tar.gz` file holding:

- `grimoire.db`: a consistent snapshot of the database (taken with SQLite
  `VACUUM INTO`, so it is safe while the server is running);
- `images/…`: every stored photo and thumbnail;
- `manifest.json`: the format version, creation time, app and schema
  version, recipe and image counts, and the size and SHA-256 of every file.

> **The admin port has no authentication.** `/admin/backup` hands the whole
> library to anyone who can reach port 9090. Keep the `grimoire-admin`
> Service ClusterIP-only, never point an Ingress at it, and let the
> NetworkPolicy admit only the backup job (and a metrics scraper, if you
> use one).

## Schedule backups with a CronJob

The server streams backups from `GET /admin/backup` on its admin port
(`GRIMOIRE_ADMIN_LISTEN`, default `:9090`). The `grimoire backup-pull`
command, run from the same image, downloads one, verifies it end to end,
gives it its final name, and deletes the oldest backups beyond `--keep`.

1. Create a PVC for backups, e.g. `grimoire-backup`.
2. Add a CronJob like this (adjust the schedule and image):

   ```yaml
   apiVersion: batch/v1
   kind: CronJob
   metadata:
     name: grimoire-backup
   spec:
     schedule: "15 3 * * *"
     concurrencyPolicy: Forbid
     jobTemplate:
       spec:
         backoffLimit: 2
         template:
           metadata:
             labels:
               app.kubernetes.io/component: backup   # matched by the NetworkPolicy
           spec:
             restartPolicy: OnFailure
             securityContext:
               runAsNonRoot: true
               seccompProfile: {type: RuntimeDefault}
             containers:
               - name: backup-pull
                 image: ghcr.io/example/grimoire:vX.Y.Z
                 args: [backup-pull, --from, "http://grimoire-admin:9090",
                        --out, /backup, --keep, "14", --timeout, 30m]
                 securityContext:
                   readOnlyRootFilesystem: true
                   allowPrivilegeEscalation: false
                   capabilities: {drop: [ALL]}
                 volumeMounts:
                   - {name: backup, mountPath: /backup}
             volumes:
               - name: backup
                 persistentVolumeClaim: {claimName: grimoire-backup}
   ```

3. Trigger one run by hand and check the log:

   ```sh
   kubectl create job --from=cronjob/grimoire-backup grimoire-backup-manual
   kubectl logs job/grimoire-backup-manual
   # wrote /backup/grimoire-20261120T031500Z.tar.gz (schema 1, 412 recipes, 97 images, 195 files)
   ```

`backup-pull` exits non-zero and leaves no `grimoire-*.tar.gz` behind if the
download fails, is cut short, or doesn't match its manifest, so a failed
job is visible in Kubernetes and never looks like a good backup. A partial
download is kept only as a hidden `.partial-*` file while it runs.

Alert on staleness with the `grimoire_backup_last_success_timestamp`
metric from `GET /metrics` on the admin port, for example
`time() - grimoire_backup_last_success_timestamp > 36 * 3600`. It resets
when the server restarts, so alert on its absence for a day as well.

## Verify a backup

`backup-pull` already checks every file against the manifest before it
keeps a backup. To check one again later, or after copying it somewhere,
restore it into a scratch directory; nothing outside that directory is
touched:

```sh
grimoire restore --from grimoire-20261120T031500Z.tar.gz --data-dir "$(mktemp -d)/data"
```

Restore re-checks every size and SHA-256, rejects missing or extra files,
and runs SQLite's `PRAGMA integrity_check` on the database before it
reports success.

For a quick look without the binary, the archive is a normal tarball:

```sh
tar -xzOf grimoire-20261120T031500Z.tar.gz manifest.json | head -20
```

## Run a restore drill

Do this every few months, and after any change to storage, so you know the
backups work before you need them.

1. Copy the newest backup to your workstation (see the next section).
2. Restore it into an empty directory:

   ```sh
   grimoire restore --from grimoire-20261120T031500Z.tar.gz --data-dir ./drill
   ```

   It prints the backup's date, schema version, and counts.
3. Check the schema, then start a throwaway server on it:

   ```sh
   grimoire migrate --status --data-dir ./drill
   GRIMOIRE_DEV=1 GRIMOIRE_AUTH_MODE=none GRIMOIRE_BASE_URL=http://127.0.0.1:8080 \
     GRIMOIRE_LISTEN=127.0.0.1:8080 GRIMOIRE_ADMIN_LISTEN=127.0.0.1:9090 \
     GRIMOIRE_DATA_DIR=./drill grimoire serve
   ```

4. Open a few recipes, including ones with photos, and compare the recipe
   count with the live instance.
5. Stop the server and delete `./drill`.

## Restore production

1. Stop the server so nothing holds the database open:
   `kubectl scale deployment/grimoire --replicas=0`.
2. Run a one-off pod with the same image that mounts both the
   `grimoire-data` and `grimoire-backup` PVCs, then:

   ```sh
   grimoire restore --from /backup/grimoire-20261120T031500Z.tar.gz --data-dir /data --force
   ```

   Without `--force`, restore refuses a data directory that already holds
   anything (a `lost+found` directory is ignored). With `--force` it
   replaces `grimoire.db` (removing any stale `-wal`/`-shm` files) and
   `images/`, and leaves everything else alone. The archive is unpacked and
   fully verified in a staging directory first; if anything fails, the data
   directory is left exactly as it was.
3. Scale the server back up: `kubectl scale deployment/grimoire --replicas=1`.
   If the backup came from an older version, the server applies the
   missing migrations at start-up.

## Copy backups off the cluster

A backup on the same storage system as the data is not a disaster backup:
if the cluster or its storage is lost, the backups go with it. Copy them
somewhere else regularly.

**The permanent off-cluster destination is not decided yet (OQ-10).** Until
it is, copy backups by hand. For example, with a temporary pod that mounts
the backup PVC:

```sh
kubectl run backup-copy --restart=Never --image=busybox \
  --overrides='{"spec":{"volumes":[{"name":"b","persistentVolumeClaim":{"claimName":"grimoire-backup"}}],
    "containers":[{"name":"backup-copy","image":"busybox","command":["sleep","3600"],
    "volumeMounts":[{"name":"b","mountPath":"/backup"}]}]}}'
kubectl wait --for=condition=Ready pod/backup-copy
kubectl exec backup-copy -- ls /backup
kubectl cp backup-copy:/backup/grimoire-20261120T031500Z.tar.gz ./grimoire-20261120T031500Z.tar.gz
kubectl delete pod backup-copy
```

Or, from any machine that can read the backup directory, sync it to object
storage or another host with an rclone-style tool:

```sh
rclone copy /backup remote:grimoire-backups --include 'grimoire-*.tar.gz'
```

Whatever the destination, verify the copy there (see "Verify a backup")
rather than trusting that the transfer worked.
