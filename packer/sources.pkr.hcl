source "googlecompute" "velda" {
  project_id          = var.gce_project_id
  zone                = var.gce_zone
  source_image_family = "ubuntu-2404-lts-amd64"
  ssh_username        = "ubuntu"
  disk_size           = 10

  image_guest_os_features = [
    "VIRTIO_SCSI_MULTIQUEUE",
    "SEV_CAPABLE",
    "SEV_SNP_CAPABLE",
    "SEV_LIVE_MIGRATABLE",
    "SEV_LIVE_MIGRATABLE_V2",
    "IDPF",
    "TDX_CAPABLE",
    "UEFI_COMPATIBLE",
    "GVNIC"
  ]
}

source "amazon-ebs" "velda" {
  region = var.aws_region
  source_ami_filter {
    filters = {
      name                = "ubuntu-minimal/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-minimal-*"
      virtualization-type = "hvm"
      root-device-type    = "ebs"
    }
    owners      = ["099720109477"] # Canonical
    most_recent = true
  }
  ssh_username            = "ubuntu"
  ami_virtualization_type = "hvm"
  ami_regions             = var.ami_regions
  ami_groups              = local.ami_groups
  ami_users               = var.ami_users
  run_tags = {
    Name = "Packer Builder"
  }
}

source "docker" "velda" {
  image = "ubuntu:24.04"
  commit = true
  platform = "linux/amd64"
}

source "azure-arm" "velda" {
  subscription_id = var.azure_subscription_id
  resource_group_name = var.azure_resource_group
  location = var.azure_location
  use_azure_cli_auth = true

  managed_image_resource_group_name = var.azure_resource_group
  
  os_type = "Linux"
  image_publisher = "Canonical"
  image_offer = "ubuntu-24_04-lts"
  image_sku = "server"
  image_version = "latest"
}