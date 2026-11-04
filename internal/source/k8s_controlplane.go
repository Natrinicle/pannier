package source

// Entry kinds captured from a Kubernetes node. An etcd snapshot is the
// API-object keyspace only — not PVC bytes, not container images, not
// kubelet logs. Those are separate entries (k8s.pvc, k8s.workload).
const (
	KindK8sEtcd         = "k8s.etcd"
	KindK8sControlPlane = "k8s.controlplane"
	KindK8sWorkload     = "k8s.workload"
	KindK8sPVC          = "k8s.pvc"
	KindK8sDBDump       = "k8s.dbdump"
)

// Command is a capture step stored as data. The cell must not SSH into
// a live cluster from this package; the pipeline execs these locally
// when it is already on the control-plane node.
type Command struct {
	Name     string
	Argv     []string
	Env      []string
	Optional bool
	Note     string
}

type CapturePlan struct {
	Kind  string
	Steps []Command
}

const (
	// https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/
	docK8sEtcd = "https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/"
	// https://etcd.io/docs/v3.5/op-guide/recovery/
	docEtcdRecovery = "https://etcd.io/docs/v3.5/op-guide/recovery/"
)

// ControlPlanePlan is the kubeadm-shaped capture. TLS files are the
// kubeadm defaults under /etc/kubernetes/pki/etcd. Endpoints are
// loopback: the snapshot runs on the etcd member, not over the LAN.
func ControlPlanePlan() []CapturePlan {
	etcdTLS := []string{
		"--cacert=/etc/kubernetes/pki/etcd/ca.crt",
		"--cert=/etc/kubernetes/pki/etcd/server.crt",
		"--key=/etc/kubernetes/pki/etcd/server.key",
	}
	return []CapturePlan{
		{
			Kind: KindK8sEtcd,
			Steps: []Command{
				{
					Name: "etcdctl-snapshot-save",
					Env:  []string{"ETCDCTL_API=3"},
					Argv: append([]string{
						"etcdctl",
						"--endpoints=https://127.0.0.1:2379",
					}, append(etcdTLS, "snapshot", "save", "snapshot.db")...),
					// Built-in snapshot of the keyspace. Does not include
					// PVC bytes, image layers, or host logs.
					// Source: https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/#backing-up-an-etcd-cluster
					Note: "etcd keyspace only; " + docK8sEtcd,
				},
				{
					Name: "etcdutl-snapshot-status",
					Argv: []string{"etcdutl", "--write-out=table", "snapshot", "status", "snapshot.db"},
					// etcdctl snapshot status is deprecated since etcd 3.5.
					// Source: https://kubernetes.io/docs/tasks/administer-cluster/configure-upgrade-etcd/#backing-up-an-etcd-cluster
					// Source: https://etcd.io/docs/v3.5/op-guide/recovery/#status-of-a-snapshot
					Note: "verify hash/revision; " + docEtcdRecovery,
				},
			},
		},
		{
			Kind: KindK8sControlPlane,
			Steps: []Command{
				{
					Name: "tar-kubernetes-etc",
					Argv: []string{"tar", "-cf", "etc-kubernetes.tar",
						"/etc/kubernetes/pki",
						"/etc/kubernetes/manifests",
						"/etc/kubernetes/admin.conf",
						"/etc/kubernetes/kubelet.conf",
						"/etc/kubernetes/controller-manager.conf",
						"/etc/kubernetes/scheduler.conf",
					},
					Note: "pki + static pods + kubeconfig; include encryption-config if present",
				},
				{
					Name:     "tar-kubernetes-enc",
					Optional: true,
					Argv:     []string{"tar", "-rf", "etc-kubernetes.tar", "/etc/kubernetes/enc"},
					Note:     "encryption-at-rest config, when the operator created it",
				},
				{
					Name:     "tar-etc-etcd",
					Optional: true,
					Argv:     []string{"tar", "-cf", "etc-etcd.tar", "/etc/etcd"},
					Note:     "/etc/etcd is NOT a kubeadm default; capture only if present",
				},
				{
					Name: "kubelet-config",
					Argv: []string{"tar", "-cf", "kubelet-config.tar",
						"/var/lib/kubelet/config.yaml",
						"/var/lib/kubelet/kubeadm-flags.env",
						"/var/lib/kubelet/kubelet.conf",
					},
					Note: "kubelet identity; systemd drop-in next",
				},
				{
					Name:     "kubelet-systemd-dropin",
					Optional: true,
					Argv:     []string{"tar", "-rf", "kubelet-config.tar", "/etc/systemd/system/kubelet.service.d"},
				},
				{
					Name: "kubeadm-config-export",
					Argv: []string{"kubectl", "--kubeconfig=/etc/kubernetes/admin.conf",
						"-n", "kube-system", "get", "configmap", "kubeadm-config", "-o", "yaml"},
					Note: "ClusterConfiguration.dataDir must match the restored etcd dir; " + docK8sEtcd,
				},
			},
		},
	}
}
