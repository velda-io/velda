packer {
  required_plugins {
    amazon = {
      source  = "github.com/hashicorp/amazon"
      version = "~> 1"
    }
    google = {
      source  = "github.com/hashicorp/googlecompute"
      version = "~> 1"
    }
  }
}


variable "version" {
  type = string
}

variable "binary_path" {
  type = string
  default = null
}

variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "instance_type" {
  type    = string
  default = "m4.2xlarge"
}

variable "driver_version" {
  type    = string
  default = "570.172.08"
}

variable "ami_regions" {
  type    = list(string)
  default = []
}

variable "gce_project_id" {
  type    = string
}

variable "gce_zone" {
  type    = string
  default = "us-central1-b"
}

variable "gce_machine_type" {
  type    = string
  default = "e2-standard-4"
}

locals {
  isdev = startswith(var.version, "dev")
  istest = strcontains(var.version, "test") || local.isdev
  build_version = local.isdev ? "dev" : var.version
  binary_path = var.binary_path != null ? var.binary_path : "../bin/velda-${local.build_version}-linux-amd64"

  ami_groups = local.istest ? [] : ["all"]

  gce_image_guest_os_features = [
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
  version_sanitized = replace(var.version, ".", "-")
}
