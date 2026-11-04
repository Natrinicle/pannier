# Split-key S3

Off-side storage is a bag of packs, not a second live engine, unless you
explicitly pick **Live Engine Repo** in Settings (`liveRepo: true`, Object
Lock off). **Locked Packs** is the default: unique chunks only, no deletes
from the cell. The two modes cannot both be on.

## Porter (ingest) — cell

IAM example: `configs/iam-ingest.json`

- `s3:ListBucket` on the prefix
- `s3:PutObject` (and Get, to read back a pack if you must)
- **no** `s3:DeleteObject`

The cell process loads `ingestCreds` only. `ValidateCell()` errors if
`maintenanceCreds` are in that YAML.

Pack-copy algorithm (`internal/s3`):

1. List source packs (walk the local Kopia repo directory; restic pack list).
   Blob-list IDs are not filesystem paths, so Locked Packs must not treat
   them as RelPaths.
2. List destination keys (path-style HTTP S3 or `file://`)
3. Put keys that are missing (SigV4 List+Put; ingest credentials from files)
4. Never delete extras
5. Never Head — write-only Object-Lock policies often deny HeadObject,
   and List already answered membership
6. Live Engine Repo (`liveRepo: true`) may call `kopia repository sync-to s3`
   with `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` in the environment,
   never `--access-key` on argv

## Steward (GC) — other host

IAM example: `configs/iam-gc.json`

- DeleteObject
- lifecycle configuration

`pannier-maint` is the only binary that should load those keys. It
refuses a cell ingest-only config.

## Object Lock is not a live Kopia repo

`kopia repository sync-to` wants a destination it can treat as a
repository: overwrite, delete extras, write blob-retention metadata.
That fights Object Lock.

kopia#3759 (`sync-to command and object locks`): filesystem → S3 with
object lock fails with `blob-retention: unsupported put-blob option`,
and lock extension is a maintenance concern on the *locked* store, which
a local filesystem repo does not have.

So:

- `objectLock: true` or `liveRepo: false` → list+put pack-copy, **must
  not** call `repository sync-to`
- `liveRepo: true` and not object-locked → engine-native copy/sync-to
  is allowed (restic `copy` only if live)

Write-only Object-Lock is a bag of unique packs. It is not a repo you
can `kopia repository connect` and prune from the cell.
