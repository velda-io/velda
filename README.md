<a href="https://velda.io">
    <img width="2334" height="1206" alt="Velda banner image" src="https://github.com/user-attachments/assets/31e8ba86-df2b-4be3-a43d-7b61d1a3b6f0" />
</a>

# Velda

Velda is a cloud-native development, workload orchestration & HPC (High Performance Computing) platform. Directly scale your application from your development environment, no extra setup required.

## Code on Velda

Velda provides a seamless development experience.
* Connect with your favorite IDE (e.g., SSH, VS Code, Cursor, Windsurf), or code and run directly from your browser (hosted or enterprise only).
* Onboard new developers instantly by cloning from a pre-configured image, or customize your own.
* Compatible with most libraries, tools, or package managers. All your environment modifications and customizations are persisted and isolated from other users.

## Scale in seconds
Velda provides the simplest way to scale or run workloads with different hardware requirements:
* `vrun` is all you need. Prefix `vrun` to your command, and run your workload with the resource you requested.
* Run any workload: machine learning training, batch processing, or host a microservice cluster.
* Unbounded capacity: Access as many machines as you need from your cloud provider.
* Your environment is always consistent. All your code, data, dependencies, and environment will be mounted on the new machine.

## Save $$$
Save instantly with Velda:
* No more idle GPUs, sandboxes, or machines. Only allocate the resources you need. Stop paying for GPUs while coding or in meetings.
* Save engineering time building, updating, and maintaining container images.
* Optionally, skip Kubernetes cluster management and scale directly with VMs from your cloud provider.

# Getting started
## Using mini-velda
[Mini-velda](/docs/mini-velda.md) runs a Velda sandbox directly from your workstation, and automatically configure your cluster to scale to the cloud. Also see [limits and restrictions](/docs/mini-velda.md#Limitations).

To start a mini-velda cluster:
```
# Init the sandbox
velda mini init sandbox

# Connect to the sandbox
ssh mini-velda

# In mini-velda sandbox, setup environment as usual
sudo apt install python3
pip install torch

# Run workload with L4 GPUs
vrun -P aws:g6.xlarge train.sh
```

Currently support automatic configuration from AWS environment, and manual configuration for GCP, K8s and command based backend.

## Set-up a shared cluster
For organizations who want sharing the cluster resource or centralized management, or needs more than mini-velda provides, you may deploy a standalone Velda cluster that is shared with team-members.
We support various deployment methods.
* Set up a new cluster:
  * [Directly on machines](docs/cluster_setup.md)
  * [Google Cloud](docs/terraform_gcp.md)
  * AWS & other cloud providers coming soon.
* [Connect to a cluster](docs/connect.md)

# 🤝 Contributing
We love contributions from our community ❤️. Pull requests are welcome!

# 👥 Community
We are super excited for community contributions of all kinds - whether it's code improvements, documentation updates, issue reports, feature requests, or discussions in our Discord.

Join our community here:

* 🌟 [Star us on GitHub](https://github.com/velda-io/velda)
* 👋 [Join our Discord community](https://discord.gg/MJQbeE33)
* 📜 [Read our blog posts](https://blog.velda.io)

# Learn more
Check out [velda.io](https://velda.io) to learn more about Velda and our hosted/enterprise offerings.
