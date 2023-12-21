# Contributing

## Building

Pre-requisites:

- make
- Go compiler

To build the controller use the following command:

```console
make all
```

To run the unit tests:

```console
make test
```

## Development

To run the controller for development purposes, you can use [Tilt](https://tilt.dev/).

### Pre-requisites

Please follow the [Tilt installation documentation](https://docs.tilt.dev/install.html) to install the command line tool.

A development Kubernetes cluster is needed to run the controller.
You can use [k3d](https://k3d.io/) to create a local cluster for development purposes.

### Settings

The `tilt-settings.yaml.example` acts as a template for the `tilt-settings.yaml` file that you need to create in the root of this repository.
Copy the example file and edit it to match your environment.
The `tilt-settings.yaml` file is ignored by git, so you can safely edit it without worrying about committing it by mistake.

The following settings can be configured:

- `registry`: the container registry where the controller image will be pushed.
  If you don't have a private registry, you can use `ghcr.io` as long as your
  cluster has access to it.

Example:

```yaml
registry: ghcr.io/your-gh-username/kwasm-operator
```

### Running the controller

The `Tiltfile` included in this repository will take care of the following:

- Create the `kwasm` namespace and install the controller helm-chart in it.
- Inject the development image in the deployment.
- Automatically reload the controller when you make changes to the code.

To run the controller, you just need to run the following command against an empty cluster:

```console
$ tilt up --stream
```
