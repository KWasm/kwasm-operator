# containerd-shim-lifecycle-operator

The containerd-shim-lifecycle-operator is the spiritual successor to the kwasm-operator. kwasm has been developed as an experimental, simple way to install Wasm runtimes. This experiment has been relatively successful, as more and more users utilzed it to fiddle around with Wasm on Kubernetes. However, the kwasm-operator has some limitations that make it difficult to use in production. The containerd-shim-lifecycle-operator is an attempt to address these limitations to make it a reliable and secure way to deploy arbitrary containerd shims.

The implementation of containerd-shim-lifecycle-operator follows [this](https://hackmd.io/TwC8Fc8wTCKdoWlgNOqTgA) community proposal.

The name should be treated as a working title and is hopefully subject to change.

## Roadmap

- tbd

## Usage

### To Deploy on the cluster

**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/kwasm-operator:tag
```

**NOTE:** This image ought to be published in the personal registry you specified. 
And it is required to have access to pull the image from the working environment. 
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/kwasm-operator:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin 
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall
**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## License

Copyright 2024.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

