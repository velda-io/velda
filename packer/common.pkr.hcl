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

variable "driver_version" {
  type    = string
  default = "570.172.08"
}

variable "ami_regions" {
  type    = list(string)
  default = []
}

variable "ami_users" {
  type = list(string)
  default = []
}

variable "gce_project_id" {
  type    = string
  default = null
}

variable "gce_zone" {
  type    = string
  default = "us-central1-b"
}

variable "agent_extra_files" {
  type    = list(string)
  default = []
}

locals {
  isdev = startswith(var.version, "dev")
  istest = strcontains(var.version, "test") || local.isdev
  build_version = local.isdev ? "dev" : var.version
  binary_path = var.binary_path != null ? var.binary_path : "bin/velda-${local.build_version}-linux-amd64"

  ami_groups = local.istest || length(var.ami_users) > 0 ? [] : ["all"]

  version_sanitized = replace(var.version, ".", "-")
}
