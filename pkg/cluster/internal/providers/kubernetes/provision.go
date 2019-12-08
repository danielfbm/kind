package kubernetes

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"text/template"
	"time"

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

	//  create context with deadline
	ctx, _ := context.WithDeadline(context.TODO(), time.Now().Add(time.Minute*5))
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
			createContainerFuncs = append(createContainerFuncs, func() error {
				// getPodTemplate(node, name, cluster)
				node.ExtraPortMappings = append(node.ExtraPortMappings,
					config.PortMapping{
						ListenAddress: "0.0.0.0",
						HostPort:      common.APIServerInternalPort,
						ContainerPort: common.APIServerInternalPort,
					},
				)
				command := createCommandForNode(node, name, cluster)
				if err := createPod(command); err != nil {
					return err
				}
				return waitUntilRead(ctx, node, name, cluster)
			})
		case config.WorkerRole:
			createContainerFuncs = append(createContainerFuncs, func() error {
				// getPodTemplate(node, name, cluster)
				command := createCommandForNode(node, name, cluster)
				if err := createPod(command); err != nil {
					return err
				}
				return waitUntilRead(ctx, node, name, cluster)
			})
		default:
			return nil, errors.Errorf("unknown node role: %q", node.Role)
		}
	}
	return
}

func getPodTemplate(node *config.Node, name, cluster string) corev1.Pod {
	trueBool := true
	basicPod := corev1.Pod{
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
					VolumeMounts: []corev1.VolumeMount{
						corev1.VolumeMount{
							Name:      "var",
							MountPath: "/var",
						},
						corev1.VolumeMount{
							Name:      "run",
							MountPath: "/run",
						},
						corev1.VolumeMount{
							Name:      "modules",
							MountPath: "/lib/modules",
							ReadOnly:  true,
						},
					},
					Ports: []corev1.ContainerPort{
						corev1.ContainerPort{},
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
					Name: "run",
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

	return basicPod
}

func createPod(command string) error {
	if err := exec.Command(command).Run(); err != nil {
		return errors.Wrap(err, "kubectl apply error")
	}
	return nil
}

func waitUntilRead(ctx context.Context, node *config.Node, name, cluster string) error {

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(time.Minute * 30)
	}
	done := make(chan error)
	deadlineTimer := time.NewTimer(deadline.Sub(time.Now()))
	everySecond := time.Tick(time.Second)
	go func() {
		for range everySecond {
			output, err := exec.Command("kubectl", "get", "pod", name, "--no-headers", "|", "awk", "'{print $3}|").Output()
			if err != nil {
				done <- err
				break
			}
			switch string(output) {
			case "Running":
				done <- nil
				break
			case "CrashLoopBackOff":
				done <- errors.Errorf("pod %s is crashing", name)
				break
			default:
				// NO-OP
			}
		}
	}()
	select {
	case <-deadlineTimer.C:
		return errors.Errorf("waiting pod %s reached deadline: %s", name, deadline)
	case err := <-done:
		return err
	}
}

func createCommandForNode(node *config.Node, name, cluster string) (command string) {
	tem, _ := podTemplateInst.Parse(podTemplate)
	writer := &bytes.Buffer{}
	err := tem.Execute(writer, map[string]interface{}{
		"name": name,
		"labels": map[string]string{
			clusterLabelKey:  cluster,
			nodeRoleLabelKey: string(node.Role),
		},
		"image":   node.Image,
		"volumes": node.ExtraMounts,
		"ports":   node.ExtraPortMappings,
	})
	if err != nil {
		panic(err)
	}
	command = writer.String()
	return
}

var podTemplateInst = template.New("podTemplate")

const podTemplate = `
cat <<EOF | kubectl apply -f -
kind: Pod
apiVersion: v1
metadata:
    name: {{.name}}
    labels:
        {{- range $key, $val := .labels }}
        {{$key}}: {{$val}}
        {{- end }}
spec:
    hostname: {{.name}}
    containers:
    - name: {{.name}}
      image: {{.image}}
      tty: true
      stdin: true
      securityContext:
          privileged: true
      volumeMounts:
      - name: var
        mountPath: /var
      - name: run
        mountPath: /run
      - name: modules
        mountPath: /lib/modules
        readOnly: true
    volumes:
    - name: var
      emptyDir: {}
    - name: run
      emptyDir: {}
    - name: modules
      hostPath:
        path: /lib/modules
---
kind: Service
apiVersion: v1
metadata:
    name: {{.name}}
spec:
    selector:
        {{- range $key, $val := .labels }}
        {{$key}}: {{$val}}
		{{- end }}
	{{- if .ports}}
    ports:
    {{- range $key, $val := .ports }}
    - protocol: TCP
      port: {{$val.ContainerPort}}
      targetPort: {{$val.ContainerPort}}
	{{- end }}
	{{- end }}
    type: NodePort
EOF
`
