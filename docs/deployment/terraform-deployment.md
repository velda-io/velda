# Deploying Velda using terraform

Follow this guide to deploy a new Velda cluster in various cloud providers. It should only take a few minutes.

## Prerequisites
* Choose your cloud provider, identify the VPC subnet.
* Terraform installed. Check out the [terraform](https://developer.hashicorp.com/terraform/install) page to download terraform.
* Permissions to apply the change. You likely need owner privilege for the initial deployment, because new roles are created for the Velda controller to create/delete instances.

## Set up
See [available terraform modules](https://github.com/velda-io/velda-terraform)

### Google cloud
```
mkdir velda-deploy && cd velda-deploy
curl -o main.tf https://raw.githubusercontent.com/velda-io/velda-terraform/refs/heads/main/gcp/examples/simple.tf
```
[More examples](https://github.com/velda-io/velda-terraform/tree/main/gcp/examples)

### AWS
```
mkdir velda-deploy && cd velda-deploy
curl -o main.tf https://raw.githubusercontent.com/velda-io/velda-terraform/refs/heads/main/aws/examples/simple.tf
```
[More examples](https://github.com/velda-io/velda-terraform/tree/main/aws/examples)

### Nebius
Check out [examples](https://github.com/velda-io/velda-terraform/blob/main/nebius/examples/simple.tf)

### Azure
Check out [definition](https://github.com/velda-io/velda-terraform/tree/main/azure). More details to follow.

## Apply the change
Make necessary changes to the terraform settings, based on the required variables of the provider.
For most cases, you need to define the resource project, subnet and pools.
```bash
terraform init
terraform apply
```

## Controller setup
Terraform will create a controller instance, and you may find the IP from your cloud console or
terraform output.

You can access the server through the IP and the SSH keys provided when provisioning the instance.

The following users are available for you to connect to the controller.

* `velda-admin`: User with `sudo` access, this is the user to manage the entire cluster or access
the controller directly. Accessible with admin key.
* `velda`: Login user, use that to access velda instance directly. Use access-key to connect to it.
* `velda_jump`: The jump-proxy for forwarding when connecting from the CLI. Used if you would like the agent nodes not exposed through public IP. Accessible with the access-key.

## Initialize your first instance
```bash
ssh velda-admin@[controller-ip]

# Create a new instance from a container image, e.g. ubuntu:24.04
# You can install dependencies later in your instance
velda instance create -d [docker-image] [instance-name]
# Example
velda instance create -d ubuntu:24.04 first
```

## Connect to your first instance
Easiest approach is to use `velda` user to login, and set the VELDA_INSTANCE.
```bash
ssh -o SetEnv=VELDA_INSTANCE=first velda@[controller-ip]
```

For other options, check [connection guide](connecting-to-cluster.md)