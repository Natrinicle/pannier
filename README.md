# Pannier

Pannier is a local-first backup console for Docker, Kubernetes, LXC, and host paths. We were already designing a scheduled, cluster-aware backup (CRON plus kubeadm etcd/PKI, dump-first databases, a local repository with unique packs copied to S3) when we found BombVault. Its public UI — one list of entries, Overview / Anomalies / Backups / Restore / Settings, live writes, a 3-2-1 picture, restore-beside, and an emergency path — is the interaction model we adopted.

Pannier is a separate implementation under LGPL-3.0-or-later. Engines are pluggable subprocesses (Kopia by default; restic and borg next). Object storage uses two keys: an ingest key that cannot delete, and a maintenance key used only for GC. BombVault inspired the screens; it is not a fork, and we do not ship its source.

A pannier is a saddlebag. The local bag fills first. The off-side bag only gets the unique packs.

## What v0 does

- Go daemon `pannierd` with an embedded HTML/JS console on loopback (`127.0.0.1:3477`). WAN binds are refused.
- Engines via exec: `kopia`, `restic`, `borg`, plus an in-memory `fake` engine so `make run` works without those binaries.
- Local repository first, then pack-copy of missing objects to S3 (list + put). Ingest keys never delete. GC lives in `pannier-maint` on a different host with a different key.
- Web UI for S3 locations, off-side mode (Locked Packs vs Live Engine Repo), cron schedule, which runtimes to bag, named Docker containers, compose projects, Kubernetes namespaces, and dump-first databases. The house bag uses one shared engine so a whole cluster or several namespaces share dedup. Optional tenant copies each get their own engine directory and remotes. Fields PATCH themselves. There is no Save button.
- Backups is a snapshot list. Open a bag to restore the whole run, one container, or one database beside the live tree. Control-plane etcd stays a sourced sequence, not a live-data-dir button.
- Kubernetes capture plan as data: `etcdctl snapshot save` on loopback TLS, `etcdutl snapshot status`, `/etc/kubernetes` (pki, manifests, kubeconfigs), kubelet config, kubeadm-config, and `/etc/etcd` **if that tree is present**.
- Self-include: redacted tool config is snapshotted with the rest of the bag.
- Dump-first PostgreSQL / MySQL / MariaDB (a dump failure fails that entry only).
- Compose named bind volume at `${HOME}/.var/pannier`, systemd timers, and a deny-egress NetworkPolicy for later start-test restores.

## What v0 does not do yet

In-cluster k8s workload/PVC discover is a stub. HTTP S3 uses a `file://` test double. Automated etcd restore is documented, not a button (wrong order corrupts the live cluster). Start-test restore into an isolated namespace is planned. Android, pairing, Unraid CA, and a Svelte rewrite are later. Docker lists containers from the local unix socket (`unix://` only; `tcp://` DOCKER_HOST is refused). Overlay2 layers stay out of the bag. Host (bare metal) is this node's paths; other Swarm or Kubernetes nodes need their own cell.

## Quick start

```sh
# console password also lands in $PANNIER_DATA_DIR/console.password if unset
export PANNIER_PASSWORD='choose-a-local-secret'
make test
make run
# open http://127.0.0.1:3477/
```

Copy `configs/pannier.example.yaml` onto a cell when you want a real Kopia repo. Copy `configs/pannier-maint.example.yaml` only onto the maintenance host. `ValidateCell()` errors if Steward (GC) keys appear in the cell file.

```sh
mkdir -p "${HOME}/.var/pannier"
# then, from deploy/compose (host uid so the bind volume is writable):
PANNIER_UID="$(id -u)" PANNIER_GID="$(id -g)" docker compose up --build
```

The container process binds `0.0.0.0:3477` inside its netns (`PANNIER_CONTAINER_BIND=1`). Compose still publishes only `127.0.0.1:3477` on the host.

## Split-key S3

B2 and similar object stores charge extra for reads and deletes. Cells get a Porter key that can list and put. A separate cron on a non-cell host runs `pannier-maint` with a Steward key that can forget and prune. Write-only Object Lock prefixes are **not** a live Kopia repository — `kopia repository sync-to` needs HEAD/read, and that 401s on ingest-only keys. Pack-copy lists source, lists dest, and puts the missing packs. See [docs/SPLIT-KEY-S3.md](docs/SPLIT-KEY-S3.md).

## Kubernetes restore

An etcd snapshot is the API-object keyspace. It does not include PVC bytes, image layers, or kubelet logs. Restore is a sourced sequence (stop API servers, restore into a **new** data-dir, retarget `etcd.yaml` and kubeadm-config). See [docs/K8S-RESTORE.md](docs/K8S-RESTORE.md).

## License

[LGPL-3.0-or-later](LICENSE). LGPLv3 is additional permissions on GPLv3; the GNU GPL text is [LICENSES/GPL-3.0.txt](LICENSES/GPL-3.0.txt). A GNU-named copy of the Lesser license is [COPYING.LESSER](COPYING.LESSER). Kopia, restic, and Borg stay optional runtime binaries with their own licenses; this program execs them and does not link them. Provenance notes are in [NOTICE](NOTICE).
