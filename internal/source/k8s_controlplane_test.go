package source

import (
	"strings"
	"testing"
)

func TestControlPlanePlanCommands(t *testing.T) {
	plans := ControlPlanePlan()
	joined := ""
	kinds := map[string]bool{}
	for _, p := range plans {
		kinds[p.Kind] = true
		for _, s := range p.Steps {
			joined += strings.Join(s.Argv, " ") + "\n" + strings.Join(s.Env, " ") + "\n"
		}
	}
	if !kinds[KindK8sEtcd] || !kinds[KindK8sControlPlane] {
		t.Fatalf("kinds %v", kinds)
	}
	wants := []string{
		"etcdctl",
		"snapshot save",
		"https://127.0.0.1:2379",
		"/etc/kubernetes/pki/etcd/ca.crt",
		"/etc/kubernetes/pki/etcd/server.crt",
		"/etc/kubernetes/pki/etcd/server.key",
		"ETCDCTL_API=3",
		"etcdutl",
		"snapshot status",
		"/etc/kubernetes/pki",
		"/etc/kubernetes/manifests",
		"admin.conf",
		"kubelet.conf",
		"controller-manager.conf",
		"scheduler.conf",
		"/etc/etcd",
		"config.yaml",
		"kubeadm-flags.env",
		"kubeadm-config",
	}
	for _, w := range wants {
		if !strings.Contains(joined, w) {
			t.Fatalf("missing %q in plan", w)
		}
	}
	if strings.Contains(joined, "pvc") && strings.Contains(joined, "snapshot save") {
		// etcd snapshot argv must not grow PVC paths
	}
}
