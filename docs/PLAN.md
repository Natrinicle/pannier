# Plan — v0 vs later

## v0 (this tree)

- Go daemon `pannierd` + embedded HTML/JS console (not React, not Svelte yet)
- Engines via exec: kopia (default), restic, borg, plus in-memory `fake`
- Split-key remotes, pack-copy list+put, cell vs maint binaries
- Pipeline lists Docker containers from the local unix socket and LXC dirs; k8s workload/PVC discover stays a stub
- k8s control-plane *capture plan* as data; restore of etcd is a sourced sequence inside the bag detail, not a live-data-dir button
- Backups lists bags (one run each). Open a bag to see parcels and restore selected items beside the live tree
- House shared engine plus optional tenant satchels (separate engine directory + remotes). House still bags whole cluster or any mix of namespaces/containers. Tenant copies never include etcd. Host paths are this node only.
- Loopback HTTP, generated password, live PATCH, no Save button
- Compose bind volume, systemd timers, deny-egress NetworkPolicy for start-test

## Not v0

- Android client
- Pairing / walk-up device join
- 42 locales
- MCP server
- Unraid Community Apps plugin
- Svelte 5 rewrite of the console
- Real AWS SDK S3 client (file:// dest is the test double)
- In-cluster k8s workload/PVC discover
- Automated etcd restore (too easy to point at the live data-dir)
- Libvirt / Unraid flash / ZFS replica lanes

## v1 cut list

1. Wire in-cluster k8s discover behind the same parcel table
2. HTTP S3 pack-copy with Porter keys, still no delete
3. Start-test restore into `restore-start-test` + the deny-egress policy
4. Console rewrite (Svelte) without changing the JSON API
5. Optional live Kopia/restic dest when the operator really wants a
   second repo instead of Object Lock
