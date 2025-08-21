# Deploying Velda using terraform

Follow this guide to deploy a new Velda cluster in various cloud providers. It should only take a few minutes.

## Prerequisites
* Choose a VPC subnet. Your client must be able to directly connect to the subnet (e.g., VPN, bastion host). 
* You need to have necessary permissions to apply the change.

## Installation
### Install terraform
Check out the [terraform](https://developer.hashicorp.com/terraform/install) page to download terraform.

## Checkout the provider & examples
See [available terraform modules](https://github.com/velda-io/velda-terraform)

## Prepare the repo
For GCP example:
```
mkdir velda-deploy && cd velda-deploy
curl -o main.tf https://raw.githubusercontent.com/velda-io/velda-terraform/refs/heads/main/gcp/examples/getting_started.tf
```
Make necessary modifications or [check out other examples](https://github.com/velda-io/velda-terraform/tree/main/gcp/examples) if needed.

## Apply the change
```bash
terraform init
terraform apply
```

You're all set. Check the printed instructions and follow the [connect guide](connect.md#create-and-initialize-your-first-instance) on how to connect to your instance.