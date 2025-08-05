<a href="https://velda.io">
    <img width="2334" height="1206" alt="Velda banner image" src="https://github.com/user-attachments/assets/31e8ba86-df2b-4be3-a43d-7b61d1a3b6f0" />
</a>

# Velda

Velda is a cloud-native development & HPC (High Performance Computing) environment.

## Scale your workload in one second:
Velda allows each developer to create their individualized development-sandbox, and run workloads with additional compute nodes in one command.

1. **Create your "Instance".**
Instance is similar to a Virtual Machine, but the compute is dynamically allocated and runs like a container. Creating an instance will create the disk, but not immediately allocate any compute resource.

2. **Connect to the instance.**
Use ssh or IDE to connect to your instance. During connection, your instance will be attached to a compute node, which is typically lightweight to run your IDE services.

3. **See the magic**
Run any command with `vrun` prefix: It will allocate additional compute nodes per your request (can be GPU) and run the command, and shutdown automatically. All your code, data, dependencies will be synced, input/output streamed, same experience like running them locally. Example:
```
vrun -P gpu ./training_script.sh
```

## Features
* **Simplicity**: `vrun` prefix is all you need to scale. Similar experience like running it locally.
* **Efficiency**: Skip the time-consuming steps of building container images, or push/pull image cycles. Your workload starts in less than a second.
* **Customizable & consistent environment**: Every developer may customize their environment and will not affect others.
Velda is compatible with most package managers and frameworks and does not require any restriction on your workload.
On the other hand, all sessions created by `vrun` will share the exact same environment as your launching instance. This eliminates any discrepancy between your local run VS running in the cluster.

* **Scale ondemand**: All the compute nodes can be dynamically allocated based on requests. It can scale up to a few thousands nodes, and no waste or idle nodes when not in use.

* **Run anywhere**: The compute nodes can be anywhere, including major cloud providers (e.g. AWS, Google Cloud), on-prem, or any Kubernetes cluster.
* **Instant onboarding**: Onboard new developers instantly from a pre-built image.

## Getting started
* [Setup a new cluster](docs/cluster_setup.md)
* [Connect to a cluster](docs/connect.md)

## 🤝 Contributing
We love contributions from our community ❤️. Pull requests are welcome!

## 👥 Community
We are super excited for community contributions of all kinds - whether it's code improvements, documentation updates, issue reports, feature requests, and discussions in our Discord.

Join our community here:

- 🌟 [Star us on GitHub](https://github.com/velda-io/velda)
- 👋 [Join our Discord community](https://discord.gg/MJQbeE33)
- 📜 [Read our blog posts](https://blog.velda.io)
