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

variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "instance_type" {
  type    = string
  default = "t2.micro"
}

variable "ssh_username" {
  type    = string
  default = "ubuntu"
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
  default = "e2-medium"
}

source "googlecompute" "velda-agent" {
  project_id             = var.gce_project_id
  zone                   = var.gce_zone
  source_image_family    = "ubuntu-2404-lts-amd64"
  machine_type           = var.machine_type
  ssh_username           = var.ssh_username
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
      name                = "ubuntu/images/hvm-ssd-gp3/ubuntu-noble-24.04-amd64-server-*"
      virtualization-type = "hvm"
      root-device-type    = "ebs"
    }
    owners      = ["099720109477"] # Canonical
    most_recent = true
  }
  instance_type   = var.instance_type
  ssh_username    = var.ssh_username
  ami_name        = "velda-agent-${var.version}"
  ami_virtualization_type = "hvm"
  ami_description = "AMI for Velda Agent"
  ami_regions     = var.ami_regions
  ami_groups       = ["all"]
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

  // Remove snapd for faster boot time.
  provisioner "shell" {
    only = ["googlecompute.velda-agent"]
    inline = [
      "sudo apt purge snapd -y",
      "sudo systemctl disable getty@tty1.service",
    ]
  }

  provisioner "shell" {
    inline = [
      "sudo apt-get update",
      "sudo apt-get install -y nfs-common --no-install-recommends",
      "sudo mkdir -p /tmp/agentdisk",
      "mkdir /tmp/velda-install"
    ]
  }

  provisioner "file" {
    source      = "../bin/velda-amd64"
    destination = "/tmp/velda-install/client"
  }

  provisioner "file" {
    only        = ["googlecompute.velda-agent"]
    source      = "ops_agent_config.yaml"
    destination = "/tmp/velda-install/ops_agent_config.yaml"
  }

  provisioner "file" {
    content     = <<EOF
[Unit]
Description=Velda agent

# Wait until the config is provided.
After=cloud-init.service google-startup-scripts.service

After=nvidia-init.service

[Service]
Type=simple
ExecStart=/bin/velda-agent agent daemon
Restart=always
RestartSec=5
Environment="PATH=/usr/bin:/bin:/snap/bin"
Environment="HOME=/root"
StandardError=journal
OOMPolicy=continue
LimitNOFILE=524288:524288

[Install]
WantedBy=network-online.target
EOF
    destination = "/tmp/velda-install/velda-agent.service"
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
      "sudo cp /tmp/velda-install/client /bin/velda-agent",
      "sudo cp /tmp/velda-install/velda-agent.service /usr/lib/systemd/system/velda-agent.service",
      "sudo rm -rf /tmp/velda-install",
      "sudo systemctl daemon-reload",
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
