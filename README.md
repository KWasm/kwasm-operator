# kwasm-operator
This Kubernetes Operators uses [KWasm/kwasm-node-installer](https://github.com/KWasm/kwasm-node-installer) to add WebAssembly support to your Kubernetes Nodes. It works with local and managed cloud K8s distributions based on Ubuntu/Debian with Containerd, including [MiniKube, MicroK8s, AKS, GKE, and EKS](https://github.com/KWasm/kwasm-node-installer#supported-kubernetes-distributions).

> **Warning**
> Only for development or evaluation purpose. Your nodes may get damaged!

> **Note**
> If you are searching for a production ready WebAssembly integration for your Kubernetes cluster, reach out to [Liquid Reply](https://www.reply.com/liquid-reply/en/)

## Example
With the KWasm Operator it is possile to have a more fine grained controll on the node provisioning in contrast of using the node-installer with a DaemonSet. In this example we create a KinD cluster with three nodes and install the KWasm Operator. A single node will be provisioned and a wasm pod will be scheduled on exactly that node. 
```bash
# Create cluster
kind create cluster --config examples/kind/cluster.yaml
# Add helm repo
helm repo add kwasm http://kwasm.sh/kwasm-operator/
# Install operator
helm install -n kwasm --create-namespace kwasm-operator kwasm/kwasm-operator 
# Annotate single node
kubectl annotate node kind-worker2 kwasm.sh/kwasm-node=true
# Run exmple
kubectl apply -f examples/kind/runtimeclass.yaml
kubectl apply -f examples/kind/pod.yaml
```

## Troubeshoot 
`Version v3.8.7 does not exist or is not available for darwin/arm64.`
```
export KUSTOMIZE_VERSION=v4.5.7
make install run
```