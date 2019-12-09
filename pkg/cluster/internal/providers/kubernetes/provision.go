package kubernetes

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"sigs.k8s.io/kind/pkg/cluster/internal/providers/provider/common"
	"sigs.k8s.io/kind/pkg/errors"
	"sigs.k8s.io/kind/pkg/internal/apis/config"
	"sigs.k8s.io/kind/pkg/log"
)

// planCreation creates a slice of funcs that will create the containers
func planCreation(logger log.Logger, cluster string, cfg *config.Cluster) (createContainerFuncs []func() error, err error) {
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
				if err := createPodForNode(logger, node, name, cluster); err != nil {
					return err
				}
				return waitUntilRead(logger, ctx, node, name, cluster)
			})
		case config.WorkerRole:
			createContainerFuncs = append(createContainerFuncs, func() error {
				// getPodTemplate(node, name, cluster)
				if err := createPodForNode(logger, node, name, cluster); err != nil {
					return err
				}
				return waitUntilRead(logger, ctx, node, name, cluster)
			})
		default:
			return nil, errors.Errorf("unknown node role: %q", node.Role)
		}
	}
	return
}

// func createPod(command string) error {
// 	if err := exec.Command(command).Run(); err != nil {
// 		return errors.Wrap(err, "kubectl apply error")
// 	}
// 	return nil
// }

func waitUntilRead(logger log.Logger, ctx context.Context, node *config.Node, name, cluster string) error {

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(time.Minute * 30)
	}
	logger.V(2).Infof("deadline for node %s is %v", name, deadline)
	done := make(chan error)
	deadlineTimer := time.NewTimer(deadline.Sub(time.Now()))
	everySecond := time.Tick(time.Second)
	go func() {
		runningTimes := 0
		targetRunningTimes := 10
		for range everySecond {
			logger.V(2).Infof("will check if pod %s is running", name)
			output, err := exec.Command("kubectl", "get", "pod", name, "--no-headers").Output()
			if err != nil {
				done <- err
				break
			}
			logger.V(2).Infof("output for pod %s is %s", name, output)
			columns := strings.Fields(string(output))
			if len(columns) < 3 {
				done <- errors.Errorf("pod %s status returned unexpected result: %s", name, output)
				break
			}
			status := columns[2]
			switch status {
			case "Running":

				runningTimes++
				if runningTimes >= targetRunningTimes {
					done <- nil
					break
				}
				// break
			case "CrashLoopBackOff":
				done <- errors.Errorf("pod %s is crashing", name)
				break
			default:
				// NO-OP, should be in a transitional state
				runningTimes = 0
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

func createPodForNode(logger log.Logger, node *config.Node, name, cluster string) (err error) {
	tem, _ := podTemplateInst.Parse(podTemplate)
	writer := &bytes.Buffer{}
	if err = tem.Execute(writer, map[string]interface{}{
		"name": name,
		"labels": map[string]string{
			clusterLabelKey:  cluster,
			nodeRoleLabelKey: string(node.Role),
		},
		"image":   node.Image,
		"volumes": node.ExtraMounts,
		"ports":   node.ExtraPortMappings,
	}); err != nil {
		return
	}
	fileName := fmt.Sprintf("%s-%s.yaml", cluster, name)
	logger.V(2).Infof("filename %s", fileName)
	logger.V(2).Infof("file content\n%s", writer.String())
	if err = ioutil.WriteFile(fileName, writer.Bytes(), os.ModePerm); err != nil {
		err = errors.Wrap(err, "write temporary file error")
		return
	}
	defer os.Remove(fileName)

	if err = exec.Command("kubectl", "apply", "-f", fileName).Run(); err != nil {
		err = errors.Wrap(err, "kubectl apply error")
	}
	return
}

var podTemplateInst = template.New("podTemplate")

const podTemplate = `kind: Pod
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
`
