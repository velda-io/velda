packer {
  required_plugins {
    amazon = {
      source  = "github.com/hashicorp/amazon"
      version = "~> 1"
    }
  }
}

variable "version" {
  type = string
}

variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "source_ami" {
  type = string
  default = ""
}

// Needs a slightly larger instance type to build the NVIDIA driver.
variable "instance_type" {
  type    = string
  default = "m4.2xlarge"
}

variable "ssh_username" {
  type    = string
  default = "ubuntu"
}

variable "driver_version" {
  type    = string
  default = "570.172.08"
}

variable "ami_users" {
  type    = list(string)
  default = []
}

variable "ami_regions" {
  type    = list(string)
  default = []
}

source "amazon-ebs" "velda-agent" {
  region          = var.aws_region
  source_ami      = var.source_ami
  instance_type   = var.instance_type
  ssh_username    = var.ssh_username
  ami_name        = "velda-agent-gpu-${var.version}"
  ami_description = "AMI for Velda GPU Agent"
  ami_regions     = var.ami_regions
  ami_users       = var.ami_users
  run_tags = {
    Name = "Packer GPU agent Builder"
  }
  tags = {
    Name = "velda-gpu-agent"
  }
}

variable "gce_project_id" {
  type    = string
  default = "skyworkstation"
}

variable "gce_zone" {
  type    = string
  default = "us-west1-a"
}

variable "source_image" {
  type    = string
  default = null
}

variable "machine_type" {
  type    = string
  default = "n1-standard-4"
}

source "googlecompute" "velda-agent" {
  project_id      = var.gce_project_id
  zone            = var.gce_zone
  source_image    = var.source_image
  machine_type    = var.machine_type
  ssh_username    = var.ssh_username
  disk_size       = 10
  image_name      = "velda-agent-gpu-${var.version}"
  image_family    = "velda-agent-gpu"
  image_description = "Image for Velda GPU Agent"
  labels = {
    name = "velda-gpu-agent"
  }
}

build {
  sources = [
    "source.amazon-ebs.velda-agent",
    "source.googlecompute.velda-agent"
  ]
  provisioner "file" {
    content     = <<EOF
[Unit]
Description=Initialize NVIDIA devices at boot

[Service]
Type=oneshot
ExecStart=/var/nvidia/bin/nvidia-smi
Environment="LD_LIBRARY_PATH=/var/nvidia/lib"

[Install]
WantedBy=sysinit.target
EOF
    destination = "/tmp/nvidia-init.service"
  }

  provisioner "shell" {
    inline = [
      // Unattended upgrader may upgrade the kernel and break the nvidia driver.
      "sudo apt update",
      "sudo apt purge unattended-upgrades -y",
      "sudo cp /tmp/nvidia-init.service /usr/lib/systemd/system/nvidia-init.service",
      "sudo systemctl enable nvidia-init.service",
      "echo Downloading nvidia driver",
      "wget -q https://developer.download.nvidia.com/compute/nvidia-driver/redist/nvidia_driver/linux-x86_64/nvidia_driver-linux-x86_64-${var.driver_version}-archive.tar.xz",
      "echo Unpacking nvidia driver",
      "tar -xf nvidia_driver-linux-x86_64-${var.driver_version}-archive.tar.xz",
      "sudo mkdir -p /var/nvidia/lib",
      "sudo mkdir -p /var/nvidia/bin",
      "echo Instaling nvidia driver for kernel $(uname -r)",
      "sudo apt install -y linux-headers-$(uname -r) gcc make",
      "sudo cp -r nvidia_driver-linux-x86_64-${var.driver_version}-archive/lib/* /var/nvidia/lib",
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

  post-processor "manifest" {
    only = ["googlecompute.velda-agent"]
    output = "gpu-gcp.json"
  }
  post-processor "manifest" {
    only = ["amazon-ebs.velda-agent"]
    output = "gpu-aws.json"
  }
}
