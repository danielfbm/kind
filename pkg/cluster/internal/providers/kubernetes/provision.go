package kubernetes

import (
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/kind/pkg/cluster/internal/providers/provider/common"
	"sigs.k8s.io/kind/pkg/errors"
	"sigs.k8s.io/kind/pkg/internal/apis/config"
)

// planCreation creates a slice of funcs that will create the containers
func planCreation(cluster string, cfg *config.Cluster) (createContainerFuncs []func() error, err error) {
	// these apply to all container creation
	nodeNamer := common.MakeNodeNamer(cluster)
	// only the external LB should reflect the port if we have multiple control planes
	// apiServerPort := cfg.Networking.APIServerPort
	// apiServerAddress := cfg.Networking.APIServerAddress

	// load balancer? if it is a kubernetes a service should suffice?
	// skip load balancer stuff

	// plan normal nodes
	for _, node := range cfg.Nodes {
		node := node.DeepCopy()              // copy so we can modify
		name := nodeNamer(string(node.Role)) // name the node

		// fixup relative paths, docker can only handle absolute paths
		for i := range node.ExtraMounts {
			hostPath := node.ExtraMounts[i].HostPath
			absHostPath, err := filepath.Abs(hostPath)
			if err != nil {
				return nil, errors.Wrapf(err, "unable to resolve absolute path for hostPath: %q", hostPath)
			}
			node.ExtraMounts[i].HostPath = absHostPath
		}

		switch node.Role {
		case config.ControlPlaneRole:

		case config.WorkerRole:
			createContainerFuncs = append(createContainerFuncs, func() error {
				getPodTemplate(node, name, cluster)
				return nil
			})
		default:
			return nil, errors.Errorf("unknown node role: %q", node.Role)
		}
	}

	return
}

func getPodTemplate(node *config.Node, name, cluster string) corev1.Pod {
	trueBool := true
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{
				clusterLabelKey:  cluster,
				nodeRoleLabelKey: string(node.Role),
			},
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyAlways,
			HostNetwork:   false,
			Hostname:      name,
			Containers: []corev1.Container{
				corev1.Container{
					Name:  name,
					Image: node.Image,
					TTY:   true,
					Stdin: true,
					SecurityContext: &corev1.SecurityContext{
						Privileged: &trueBool,
					},
				},
			},
			Volumes: []corev1.Volume{
				corev1.Volume{
					Name: "var",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				corev1.Volume{
					Name: "modules",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{
							Path: "/lib/modules",
						},
					},
				},
			},
		},
	}
}
