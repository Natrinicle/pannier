# Architecture

Pannier is a backup *orchestrator*. Engines (kopia, restic, borg) stay
separate processes. This module does not import `github.com/kopia/kopia`
or kopiur. Linking those libraries would mix licenses and pull a second
implementation of pack format into the daemon.

## Engine exec

`internal/engine` builds argv and runs it through `Runner`. Production
uses `ExecRunner` (`exec.CommandContext`). Tests use a recording runner
that stores argv. Passwords:

- kopia: password file is read in-process and passed as `-p`. Logs go
  through `RedactArgv`.
- restic: `--password-file` only. The secret never appears on argv.
- borg: `BORG_PASSPHRASE_FD=3` plus `ExtraFiles` on the exec runner.

`ForgetPrune` with `RoleIngest` is always an error. Sweeping the off-side
bag is Steward's job on another host.

## Split-key

Each remote has `ingestCreds` (Porter: list + put) and, only on the
maintenance host, `maintenanceCreds` (Steward: delete / lifecycle).
`ValidateCell()` errors if Steward keys are present in the cell YAML.
`pannier-maint` refuses the opposite: a cell ingest-only file.

Write-only Object Lock is **not** a live Kopia repository. Pack-copy is
list-source, list-dest, put-missing. Never Head, never delete. See
[SPLIT-KEY-S3.md](SPLIT-KEY-S3.md) and kopia#3759.

## Entry kinds

| Kind | What it is |
|------|------------|
| `pannier.self` | Redacted YAML, snapshotted first |
| `db.dump` | Dump command → engine stdin (failure is that entry only) |
| `host.path` | Bare-metal filesystem |
| `docker.container` | One parcel per container (`docker:name`). Bind mounts and readable named volumes. Overlay2 stays out. |
| `lxc.instance` | One parcel per `/var/lib/lxc/<name>` |
| `k8s.workload` | Discover stub in v0 |
| `k8s.pvc` | Volume bytes; **not** in the etcd snapshot |
| `k8s.etcd` | etcd keyspace via `etcdctl snapshot save` |
| `k8s.controlplane` | `/etc/kubernetes`, kubelet, kubeadm-config |

An etcd snapshot is API objects. It does not contain PVC bytes, image
layers, or kubelet logs.

## Pipeline

Numbered in `internal/pipeline/backup.go`:

1. lock
2. discover (docker.sock / `/var/lib/lxc` / include names)
3. self-include first
4. dump-first SQL (per-db failure)
5. docker / lxc (one snapshot per instance)
6. k8s pvc
7. k8s etcd
8. filesystem
9. local snapshot + first verify
10. pack-copy ingest
11. optional live remote
12. local retention
13. scheduled check
14. start-test
15. maint GC never on cell

## Console

`pannierd` listens on `127.0.0.1:3477` by default and refuses a WAN bind.
`PANNIER_CONTAINER_BIND=1` allows `0.0.0.0` *inside a container namespace*
when the host publish stays `127.0.0.1:3477`. `/healthz` is the boolean
`true` with no secrets. The console assets (`/`, `/style.css`, `/app.js`)
are unauthenticated so the login gate can render. `/v1/*` needs
`PANNIER_PASSWORD` (or a generated password printed once). Systemd timers
call `pannierd -run backup` / `-run packcopy` so the console password never
lands on argv. The embedded UI live-PATCHes YAML; there is no Save button. Off-side
mode is exclusive: Locked Packs (`objectLock`, ingest list+put) or Live
Engine Repo (`liveRepo`, needs read and delete). Backups lists bags;
restore lives in the bag detail (`POST /v1/restore`) and writes under
`$DATADIR/restore/<bag>/`. Control-plane kinds return the sourced
sequence instead of pointing at the live etcd data-dir.

## House vs tenant satchels

The top-level `localRepo` is the **shared engine**. One Kopia/restic/borg
directory can bag the whole cluster, several namespaces, several compose
projects, or several containers so they share deduplication. `Run a Bag
Now` always packs this house satchel.

`satchels:` are extra **tenant copies**. Each has its own `localRepo.path`
and its own remotes. Use them when a customer wants their own off-side
bag on their own systems. Packs are never mixed with house or with
another tenant. A tenant copy cannot include etcd (the keyspace is the
whole cluster). Kubernetes tenants need namespaces; Docker tenants need
include names or compose projects.

House remotes is a list: one shared dataset can copy unique packs to
several locations.

## This node, not the cluster

`pannierd` is one process on one host.

- Docker lists the local unix socket. Bind mounts and named volumes are
  this engine's files. Compose project is the `com.docker.compose.project`
  label on those containers. Swarm/K8S tasks on another node are not in
  this bag.
- Kubernetes etcd and `/etc/kubernetes` exist on control-plane nodes.
  Kubelet trees exist on every node. Longhorn PVC bytes are not a host
  path; that stays a `k8s.pvc` stub.
- Host (bare metal) paths are **this node's** filesystem. A cluster-wide
  host bag is one cell per node (or a later agent), not one node's `/etc`.

A customer namespace is an API scope. It is not a substitute for a
per-node host bag.
