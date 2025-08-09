# Deploying Velda on Google Cloud using terraform

Follow this guide to deploy a new Velda cluster in Google Cloud. It should only take a few minutes.

## Prerequisites
* You have a Google Cloud project. The project needs compute API enabled.
* Choose a VPC subnet. Your client must be able to directly connect to the subnet (e.g., VPN, bastion host). 
* You have the necessary permission(e.g. Project owner) to apply the change.

## Installation
### Install terraform
Check out the [terraform](https://developer.hashicorp.com/terraform/install) page to download terraform.

## Prepare the repo
```
mkdir velda-deploy && cd velda-deploy
curl -o main.tf https://raw.githubusercontent.com/velda-io/velda-terraform/refs/heads/main/gcp/examples/getting_started.tf
```
Make necessary modifications or [check out other examples](https://github.com/velda-io/velda-terraform/tree/main/gcp/examples) if needed.
For minimum, you need to update project_id.

## Apply the change
```bash
terraform init
terraform apply
```

You're all set. Check the printed instructions and follow the [connect guide](connect.md#create-and-initialize-your-first-instance) on how to connect to your instance.