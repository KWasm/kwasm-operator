## RuntimeClass

The Operator is designed to create a RuntimeClass for each shim. `spec.runtimeClass` configures the RuntimeClass that will be created.

* `spec.runtimeClass.name`: Name of the Kubernetes RuntimeClass
* `spec.runtimeClass.handler`: Name of the shim as it is referenced in the containerd config

**Discuss later:**

- At this point in time `spec.RuntimeClass` is a mendatory field
    - pro: it will make sure a RuntimeClass exist for the shim thats going to be installed
    - con: possible that runtimeclass is created by other means
- Should `spec.RuntimeClass.handler` be optional? Is it even required?
