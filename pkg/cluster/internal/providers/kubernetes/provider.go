package kubernetes

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/kind/pkg/cluster/internal/providers/provider"
	"sigs.k8s.io/kind/pkg/cluster/nodes"
	"sigs.k8s.io/kind/pkg/errors"
	"sigs.k8s.io/kind/pkg/exec"
	"sigs.k8s.io/kind/pkg/internal/apis/config"
	"sigs.k8s.io/kind/pkg/internal/cli"
	"sigs.k8s.io/kind/pkg/log"
)

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
	createContainerFuncs, err := planCreation(cluster, cfg)
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
		"-o", fmt.Sprintf(`jsonpath={.items[*]['metadata.labels.%s']}`, clusterLabelKey),
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
	return nil, nil
}

// DeleteNodes deletes the provided list of nodes
// These should be from results previously returned by this provider
// E.G. by ListNodes()
func (p *Provider) DeleteNodes([]nodes.Node) error {
	return nil
}

// GetAPIServerEndpoint returns the host endpoint for the cluster's API server
func (p *Provider) GetAPIServerEndpoint(cluster string) (string, error) {
	return "", nil
}
