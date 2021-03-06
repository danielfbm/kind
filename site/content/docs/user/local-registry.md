---
title: "Local Registry"
menu:
  main:
    parent: "user"
    identifier: "user-local-registry"
    weight: 3
---
# Local Registry

With kind v0.6.0 there is a new config feature `containerdConfigPatches` that can
be leveraged to configure insecure registries.
The following recipe leverages this to enable a local registry.

## Create A Cluster And Registry

The following shell script will create a local docker registry and a kind cluster
with it enabled.

```bash
#!/bin/sh
set -o errexit

# create registry container unless it already exists 
CLUSTER_NAME="my-cluster"   # desired cluster name; default is "kind"
REGISTRY_CONTAINER_NAME='kind-registry'
REGISTRY_PORT='5000'
if [ "$(docker inspect -f '{{.State.Running}}' "${REGISTRY_CONTAINER_NAME}")" != 'true' ]; then
  docker run -d -p "${REGISTRY_PORT}:5000" --restart=always --name "${REGISTRY_CONTAINER_NAME}" registry:2
fi

# create a cluster with the local registry enabled in containerd
cat <<EOF | kind create cluster --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches: 
- |-
  [plugins."io.containerd.grpc.v1.cri".registry.mirrors."registry:${REGISTRY_PORT}"]
    endpoint = ["http://registry:${REGISTRY_PORT}"]
EOF

# add the registry to /etc/hosts on each node
for node in $(kind get nodes --name ${CLUSTER_NAME}); do
  docker exec "${node}" sh -c "echo $(docker inspect --format '{{.NetworkSettings.IPAddress }}' "${REGISTRY_CONTAINER_NAME}") registry >> /etc/hosts"
done
```

## Using The Registry

The registry can be used like this.

1. First we'll pull an image `docker pull gcr.io/google-samples/hello-app:1.0`
2. Then we'll tag the image to use the local registry `docker tag gcr.io/google-samples/hello-app:1.0 localhost:5000/hello-app:1.0`
3. Then we'll push it to the registry `docker push localhost:5000/hello-app:1.0`
4. And now we can use the image `kubectl create deployment hello-server --image=registry:5000/hello-app:1.0`

If you build your own image and tag it like `localhost:5000/image:foo` and then use
it in kubernetes as `registry:5000/image:foo`.

> Note: you may update your local hosts file as well, for example by adding `127.0.0.1 registry` in your laptop's `/etc/hosts`, so you can reference it in a consistent way by simply using `registry:5000`.
