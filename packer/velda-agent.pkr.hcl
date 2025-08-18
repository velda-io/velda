packer {
  required_plugins {
    amazon = {
      source  = "github.com/hashicorp/amazon"
      version = "~> 1"
    }
    googlecompute = {
      source  = "github.com/hashicorp/googlecompute"
      version = "~> 1"
    }
  }
}

variable "version" {
  type = string
}

locals {
  isdev = startswith(var.version, "dev")
  build_version = local.isdev ? "dev" : var.version
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
  default = "velda-oss"
}

variable "gce_zone" {
  type    = string
  default = "us-central1-b"
}

variable "machine_type" {
  type    = string
  default = "e2-standard-4"
}

source "googlecompute" "velda-agent" {
  project_id             = var.gce_project_id
  zone                   = var.gce_zone
  source_image_family    = "ubuntu-2404-lts-amd64"
  machine_type           = var.machine_type
  ssh_username           = "ubuntu"
  disk_size              = 10
  image_name             = "velda-agent-${var.version}"
  image_family           = "velda-agent"
  image_description      = "Image for Velda Agent"
  labels = {
    name = "velda-agent"
  }
}

source "amazon-ebs" "velda-agent" {
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
  instance_type   = var.instance_type
  ssh_username    = "ubuntu"
  ami_name        = "velda-agent-${var.version}"
  ami_virtualization_type = "hvm"
  ami_description = "AMI for Velda Agent"
  ami_regions     = var.ami_regions
  ami_groups       = local.isdev ? [] : ["all"]
  run_tags = {
    Name = "Packer Builder"
  }
  tags = {
    Name = "velda-agent"
  }
}

build {
  sources = [
    "source.amazon-ebs.velda-agent",
    "source.googlecompute.velda-agent"
  ]

  // Make boot faster
  provisioner "shell" {
    inline = [
      // Mask unnecessary services.
      "sudo systemctl mask getty@tty1.service",
      "sudo systemctl mask man-db.service apt-daily.service apt-daily-upgrade.service",
      "sudo systemctl mask snapd.service snapd.seeded.service",
      "sudo systemctl mask unattended-upgrades.service",
    ]
  }

  provisioner "shell" {
    inline = [
      "sudo apt-get update",
      "sudo apt-get install -y nfs-common --no-install-recommends",
      "mkdir /tmp/velda-install"
    ]
  }

  provisioner "file" {
    source      = "../bin/velda-${local.build_version}-linux-amd64"
    destination = "/tmp/velda-install/client"
  }

  provisioner "file" {
    only        = ["googlecompute.velda-agent"]
    source      = "ops_agent_config.yaml"
    destination = "/tmp/velda-install/ops_agent_config.yaml"
  }

  provisioner "file" {
    source = "./scripts/velda-agent.service"
    destination = "/tmp/velda-install/velda-agent.service"
  }
  provisioner "file" {
    source = "./scripts/nvidia-init.service"
    destination = "/tmp/velda-install/nvidia-init.service"
  }

  provisioner "shell" {
    only = ["googlecompute.velda-agent"]
    inline = [
      "curl -sSO https://dl.google.com/cloudagents/add-google-cloud-ops-agent-repo.sh",
      "sudo bash add-google-cloud-ops-agent-repo.sh --also-install",
      "rm -f add-google-cloud-ops-agent-repo.sh",
      "sudo cp /tmp/velda-install/ops_agent_config.yaml /etc/google-cloud-ops-agent/config.yaml",
    ]
  }

  provisioner "shell" {
    inline = [
      // Unattended upgrader may upgrade the kernel and break the nvidia driver.
      "echo Downloading nvidia driver",
      "wget -q https://developer.download.nvidia.com/compute/nvidia-driver/redist/nvidia_driver/linux-x86_64/nvidia_driver-linux-x86_64-${var.driver_version}-archive.tar.xz",
      "echo Unpacking nvidia driver",
      "tar -xf nvidia_driver-linux-x86_64-${var.driver_version}-archive.tar.xz",
      "sudo mkdir -p /var/nvidia/lib",
      "sudo mkdir -p /var/nvidia/bin",
      "echo Instaling nvidia driver for kernel $(uname -r)",
      "sudo apt install -y linux-headers-$(uname -r) gcc make",
      "sudo cp -r nvidia_driver-linux-x86_64-${var.driver_version}-archive/lib/* /var/nvidia/lib",
      "sudo cp -r nvidia_driver-linux-x86_64-${var.driver_version}-archive/bin/* /var/nvidia/bin",
      "sudo cp -r nvidia_driver-linux-x86_64-${var.driver_version}-archive/sbin/* /var/nvidia/bin",

      "sudo mkdir -p /var/nvidia/x11/extensions",
      "sudo mkdir -p /var/nvidia/x11/drivers",
      "sudo ln -s ../../lib/libglxserver_nvidia.so.${var.driver_version} /var/nvidia/x11/extensions/glx.so",
      "sudo ln -s ../../lib/nvidia_drv.so /var/nvidia/x11/drivers/nvidia_drv.so",
      # Create name based on SONAME. Skip if file already exists.
      "for i in /var/nvidia/lib/*.so.*; do sudo ln -s $(basename $i) $(dirname $i)/$(echo $(basename $i) | grep -o .*so)1 || true; done",
      "for i in /var/nvidia/lib/*.so.*.*; do sudo ln -s $(basename $i) $(dirname $i)/$(objdump -p $i | grep SONAME | awk '{print $2}') || true; done",
      # Some library references libcuda.so instead of SONAME, so we need to create a symlink.
      "sudo ln -s libcuda.so.1 /var/nvidia/lib/libcuda.so",
      # Build the kernel driver.
      "cd nvidia_driver-linux-x86_64-${var.driver_version}-archive/kernel && make -j $(nproc) && sudo make -j $(nproc) modules_install && sudo depmod -a",
      "rm -rf nvidia_driver-linux-x86_64-${var.driver_version}-archive*",
    ]
  }

  provisioner "shell" {
    inline = [
      "sudo cp /tmp/velda-install/client /bin/velda-agent",
      "sudo cp /tmp/velda-install/nvidia-init.service /usr/lib/systemd/system/nvidia-init.service",
      "sudo cp /tmp/velda-install/velda-agent.service /usr/lib/systemd/system/velda-agent.service",
      "sudo rm -rf /tmp/velda-install",
      "sudo systemctl daemon-reload",
      "sudo systemctl enable nvidia-init",
      "sudo systemctl enable velda-agent",
    ]
  }

  post-processor "manifest" {
    only   = ["amazon-ebs.velda-agent"]
    output = "base-aws.json"
  }
  post-processor "manifest" {
    only   = ["googlecompute.velda-agent"]
    output = "base-gcp.json"
  }
}
