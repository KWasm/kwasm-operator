# kwasm-provisioner-operator
This Kubernetes Operators installs WebAssembly support on your Kubernetes Nodes

## Example
With the KWasm Operator it is possile to have a more fine grained controll on the node provisioning. In this example we create a KinD cluster with three nodes and install the KWasm Operator. A single node will be provisioned and a wasm pod will be scheduled on exactly that node. 
```
kind create cluster --config examples/kind/cluster.yaml
make install run
kubectl annotate node kind-worker2 kwasm.sh/kwasm-node=true
kubectl apply -f examples/kind/runtimeclass.yaml
kubectl apply -f examples/kind/pod.yaml
```

## Troubeshoot 
`Version v3.8.7 does not exist or is not available for darwin/arm64.`
```
export KUSTOMIZE_VERSION=v4.5.7
make install run
```