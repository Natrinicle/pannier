# Kubernetes control-plane restore

Sourced sequence. This is not a one-click. Wrong order corrupts the live
cluster.

Sources:

- https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/
- https://etcd.io/docs/v3.5/op-guide/recovery/

## What the etcd snapshot is (and is not)

`etcdctl snapshot save` is the **keyspace** (Kubernetes API objects).
It does not include:

- PVC bytes
- container images
- kubelet logs
- `/var/lib/kubelet` pod volumes

Those are `k8s.pvc` / `k8s.workload` / host-path entries.

## Order

1. **Stop every API server first.** Kubernetes docs: do not restore etcd
   while API servers are running. Stop kube-apiserver static pods (move
   manifests out of `/etc/kubernetes/manifests` or stop kubelet with
   care). Also restart kube-scheduler, kube-controller-manager, kubelet
   after restore so they drop stale informer caches.
   https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/#restoring-an-etcd-cluster

2. **Restore into a NEW data-dir.** Do not overwrite the live member
   directory in place.

   ```sh
   etcdutl snapshot restore snapshot.db --data-dir /var/lib/etcd-restored
   ```

   Prefer `etcdutl` over `etcdctl snapshot restore` (deprecated since
   etcd 3.5). For Kubernetes watches, consider `--bump-revision` and
   `--mark-compacted` as described in the etcd recovery guide:
   https://etcd.io/docs/v3.5/op-guide/recovery/#restoring-with-revision-bump

3. **Point the static pod at that directory.** Change
   `/etc/kubernetes/manifests/etcd.yaml` `volumes.hostPath.path` for
   `name: etcd-data` to the new data-dir, then delete the etcd pod or
   restart kubelet.
   https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/#restoring-an-etcd-cluster

4. **Update kubeadm-config `dataDir`.** On a kubeadm cluster, set
   `etcd.local.dataDir` in the `kubeadm-config` ConfigMap to the new
   directory. If it still names the old path, the next `kubeadm upgrade`
   rewrites the etcd manifest back to the old dir.
   https://kubernetes.io/docs/tasks/administer-cluster/kubeadm/kubeadm-reconfigure/#updating-the-clusterconfiguration

5. **`/etc/etcd` only if it existed.** kubeadm's default is static pods
   plus `/etc/kubernetes/pki/etcd`. A non-kubeadm or extra config tree
   under `/etc/etcd` is optional capture and optional restore.

6. Restore `/etc/kubernetes` (pki, manifests, admin.conf, kubelet.conf,
   controller-manager.conf, scheduler.conf, encryption-config if present)
   and kubelet `config.yaml` / `kubeadm-flags.env` / systemd drop-in
   from the `k8s.controlplane` tarball.

## Capture (for the backup side)

Commands live as data in `internal/source/k8s_controlplane.go`. They are
meant to run on the control-plane node (loopback etcd, local TLS files),
not over SSH from this package.

```sh
ETCDCTL_API=3 etcdctl --endpoints=https://127.0.0.1:2379 \
  --cacert=/etc/kubernetes/pki/etcd/ca.crt \
  --cert=/etc/kubernetes/pki/etcd/server.crt \
  --key=/etc/kubernetes/pki/etcd/server.key \
  snapshot save snapshot.db

etcdutl --write-out=table snapshot status snapshot.db
```

TLS paths are the kubeadm defaults from
https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/#securing-communication
