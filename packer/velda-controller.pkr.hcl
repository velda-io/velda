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

variable "instance_type" {
  type    = string
  default = "t2.micro"
}

variable "ssh_username" {
  type    = string
  default = "ubuntu"
}

source "amazon-ebs" "velda-controller" {
  region = "us-east-1"
  source_ami_filter {
    filters = {
      name                = "ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"
      virtualization-type = "hvm"
      root-device-type    = "ebs"
    }
    owners      = ["099720109477"] # Canonical
    most_recent = true
  }

  instance_type   = var.instance_type
  ssh_username    = var.ssh_username
  ami_name        = "velda-controller-${var.version}"
  ami_description = "AMI for Velda controller"
  ami_users       = ["all"]
  run_tags = {
    Name = "Controller Packer Builder"
  }
  tags = {
    Name = "velda-controller"
  }
}

variable "gcp_project_id" {
  type    = string
  default = "velda-oss"
}

variable "gcp_zone" {
  type    = string
  default = "us-central1-b"
}

variable "gcp_machine_type" {
  type    = string
  default = "e2-medium"
}

variable "gcp_ssh_username" {
  type    = string
  default = "ubuntu"
}

source "googlecompute" "velda-controller" {
  project_id             = var.gcp_project_id
  source_image_family    = "ubuntu-2404-lts-amd64"
  zone                   = var.gcp_zone
  machine_type           = var.gcp_machine_type
  ssh_username           = var.gcp_ssh_username
  disk_size              = 10
  image_name             = "velda-controller-${var.version}"
  image_family           = "velda-controller"
  image_description      = "Image for Velda controller (GCP)"
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
  labels = {
    name = "velda-controller"
  }
}

build {
  sources = [
    "source.amazon-ebs.velda-controller",
    "source.googlecompute.velda-controller"
  ]

  provisioner "shell" {
    inline = [
      "sudo apt-get update",
      "sudo apt-get install -y zfsutils-linux nfs-kernel-server --no-install-recommends",
      "mkdir /tmp/velda-install",
    ]
  }

  provisioner "file" {
    source      = "../bin/apiserver-amd64"
    destination = "/tmp/velda-install/apiserver"
  }
  provisioner "file" {
    source      = "./scripts/velda-apiserver.service"
    destination = "/tmp/velda-install/velda-apiserver.service"
  }
  provisioner "file" {
    only        = ["googlecompute.velda-controller"]
    source      = "ops_agent_config.yaml"
    destination = "/tmp/velda-install/ops_agent_config.yaml"
  }
  provisioner "file" {
    source      = "./scripts/install.sh"
    destination = "/tmp/velda-install/install.sh"
  }
  provisioner "shell" {
    only = ["googlecompute.velda-controller"]
    inline = [
      "curl -sSO https://dl.google.com/cloudagents/add-google-cloud-ops-agent-repo.sh",
      "sudo bash add-google-cloud-ops-agent-repo.sh --also-install",
      "rm -f add-google-cloud-ops-agent-repo.sh",
      "sudo cp /tmp/velda-install/ops_agent_config.yaml /etc/google-cloud-ops-agent/config.yaml",
    ]
  }
  provisioner "shell" {
    inline = [
      "chmod +x /tmp/velda-install/install.sh",
      "sudo /tmp/velda-install/install.sh",
      "rm /tmp/velda-install -rf",
    ]
  }
  post-processor "manifest" {
    output = "controller.json"
  }
}
