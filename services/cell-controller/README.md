# cell-controller

The control plane's cell provisioner. Lives in the control plane's
Kubernetes cluster and applies/maintains the cell Helm chart for each
tenant cell, then registers the cell's endpoints back to the control
plane's directory.

On-premise deployments do not run this service: their cell is provisioned
by whatever the customer uses (helm CLI, Argo CD, Flux, etc.).

License: FSL-1.1-Apache-2.0.
