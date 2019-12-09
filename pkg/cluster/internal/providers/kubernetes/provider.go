package kubernetes

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/kind/pkg/cluster/internal/providers/provider"
	"sigs.k8s.io/kind/pkg/cluster/nodes"
	"sigs.k8s.io/kind/pkg/errors"
	"sigs.k8s.io/kind/pkg/exec"
	"sigs.k8s.io/kind/pkg/internal/apis/config"
	"sigs.k8s.io/kind/pkg/internal/cli"
	"sigs.k8s.io/kind/pkg/log"
)

// NewProvider returns a new provider based on executing `docker ...`
func NewProvider(logger log.Logger) provider.Provider {
	return &Provider{
		logger: logger,
	}
}

// Provider implements provider.Provider
// see NewProvider
type Provider struct {
	logger log.Logger
}

var _ provider.Provider = &Provider{}

// Provision should create and start the nodes, just short of
// actually starting up Kubernetes, based on the given cluster config
func (p *Provider) Provision(status *cli.Status, cluster string, cfg *config.Cluster) (err error) {

	// actually provision the cluster
	// TODO: strings.Repeat("📦", len(desiredNodes))
	status.Start("Preparing nodes 📦")
	defer func() { status.End(err == nil) }()

	// plan creating the containers
	createContainerFuncs, err := planCreation(p.logger, cluster, cfg)
	if err != nil {
		return err
	}

	// actually create nodes
	return errors.UntilErrorConcurrent(createContainerFuncs)
}

// ListClusters discovers the clusters that currently have resources
// under this providers
func (p *Provider) ListClusters() ([]string, error) {
	cmd := exec.Command("kubectl",
		"get",
		"pod",
		"-l", clusterLabelKey,
		// format to include the cluster name
		"-o", fmt.Sprintf(`jsonpath={.items[*]['metadata.labels.%s']}`, labelForJSONPath(clusterLabelKey)),
	)
	lines, err := exec.OutputLines(cmd)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list clusters")
	}
	return sets.NewString(lines...).List(), nil
}

// ListNodes returns the nodes under this provider for the given
// cluster name, they may or may not be running correctly
func (p *Provider) ListNodes(cluster string) ([]nodes.Node, error) {
	cmd := exec.Command("kubectl",
		"get", "pod",
		"-l", fmt.Sprintf("%s=%s", clusterLabelKey, cluster),
		// only name
		"-o", "custom-columns=NAME:.metadata.name",
		// remove header
		"--no-headers",
	)
	lines, err := exec.OutputLines(cmd)
	if err != nil {
		return nil, errors.Wrap(err, "failed to list clusters")
	}
	// convert names to node handles
	ret := make([]nodes.Node, 0, len(lines))
	for _, name := range lines {
		ret = append(ret, p.node(name))
	}
	return ret, nil
}

// DeleteNodes deletes the provided list of nodes
// These should be from results previously returned by this provider
// E.G. by ListNodes()
func (p *Provider) DeleteNodes(n []nodes.Node) error {
	if len(n) == 0 {
		return nil
	}
	const command = "kubectl"
	args := make([]string, 0, len(n)+3) // allocate once
	args = append(args,
		"delete", "pod",
		"--grace-period=1", // force the container to be delete now
		"--wait=false",     //do not wait pods to be really deleted
	)
	for _, node := range n {
		args = append(args, node.String())
	}
	if err := exec.Command(command, args...).Run(); err != nil {
		return errors.Wrap(err, "failed to delete pods")
	}

	// delete services
	args = args[:]
	args = append(args,
		"delete", "svc",
	)
	for _, node := range n {
		args = append(args, node.String())
	}
	if err := exec.Command(command, args...).Run(); err != nil {
		return errors.Wrap(err, "failed to delete services")
	}
	return nil
}

// GetAPIServerEndpoint returns the host endpoint for the cluster's API server
func (p *Provider) GetAPIServerEndpoint(cluster string) (string, error) {
	// locate the node that hosts this
	// allNodes, err := p.ListNodes(cluster)
	// if err != nil {
	// 	return "", errors.Wrap(err, "failed to list nodes")
	// }
	// n, err := nodeutils.APIServerEndpointNode(allNodes)
	// if err != nil {
	// 	return "", errors.Wrap(err, "failed to get cluster node")
	// }

	// kubectl get cluster master ip
	cmd := exec.Command("kubectl", "cluster-info")
	lines, err := exec.OutputLines(cmd)
	if err != nil {
		return "", errors.Wrap(err, "failed to get api server endpoint")
	}
	if len(lines) == 0 || !strings.Contains(lines[0], "Kubernetes master is running at ") {
		return "", errors.Wrap(err, "failed to get api server endpoint from cluster-info")
	}
	masterAddress := strings.ReplaceAll(lines[0], "Kubernetes master is running at ", "")
	// cmd = exec.Command("kubectl",
	// 	"get", "svc",
	// 	n.String(),
	// 	"-o", "jsonpath={.spec.ports[*]['nodePort']}",
	// )
	// lines, err := exec.OutputLines(cmd)
	// if err != nil {
	// 	return "", errors.Wrap(err, "failed to get api server endpoint")
	// }
	// if len(lines) != 1 {
	// 	return "", errors.Wrap(err, "failed to get api server service endpoint port")
	// }

	return masterAddress, nil
}

// node returns a new node handle for this provider
func (p *Provider) node(name string) nodes.Node {
	return &node{
		name: name,
	}
}
