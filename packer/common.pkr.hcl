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
    docker = {
      source  = "github.com/hashicorp/docker"
      version = "~> 1"
    }
    azure = {
      source  = "github.com/hashicorp/azure"
      version = "~> 2"
    }
  }
}


variable "version" {
  type = string
}

variable "binary_path" {
  type    = string
  default = null
}

variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "driver_version" {
  type    = string
  default = "580.126.20"
}

variable "ami_regions" {
  type    = list(string)
  default = []
}

variable "ami_users" {
  type    = list(string)
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

variable "docker_username" {
  type    = string
  default = null
}

variable "docker_password" {
  type    = string
  default = null
}

variable "azure_resource_group" {
  type    = string
  default = null
}

variable "azure_subscription_id" {
  type    = string
  default = null
}

variable "azure_location" {
  type    = string
  default = "East US"
}

variable "azure_shared_image_gallery" {
  type    = string
  default = null
}

variable "azure_shared_image_gallery_resource_group" {
  type    = string
  default = null
}

locals {
  isdev       = startswith(var.version, "dev")
  istest      = strcontains(var.version, "test") || local.isdev
  binary_path = var.binary_path != null ? var.binary_path : "bin/velda-${var.version}-linux-amd64"

  ami_groups = local.istest || length(var.ami_users) > 0 ? [] : ["all"]

  version_sanitized = replace(var.version, ".", "-")
}
