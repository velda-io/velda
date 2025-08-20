source "googlecompute" "velda-controller" {
  project_id          = var.gce_project_id
  source_image_family = "ubuntu-2404-lts-amd64"
  zone                = var.gce_zone
  machine_type        = var.gce_machine_type
  ssh_username        = "ubuntu"
  disk_size           = 10
  image_name          = "velda-controller-${local.version_sanitized}"
  image_family        = "velda-controller"
  image_description   = "Image for Velda controller (GCP)"
  image_guest_os_features = local.gce_image_guest_os_features
  labels = {
    name = "velda-controller"
  }
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

  instance_type           = var.instance_type
  ssh_username            = "ubuntu"
  ami_virtualization_type = "hvm"
  ami_name                = "velda-controller-${var.version}"
  ami_description         = "AMI for Velda controller"
  ami_groups              = local.ami_groups
  ami_users               = var.ami_users
  run_tags = {
    Name = "Controller Packer Builder"
  }
  tags = {
    Name = "velda-controller"
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
    source      = local.binary_path
    destination = "/tmp/velda-install/velda"
  }
  provisioner "file" {
    source      = "${path.root}/scripts/velda-apiserver.service"
    destination = "/tmp/velda-install/velda-apiserver.service"
  }
  provisioner "file" {
    only        = ["googlecompute.velda-controller"]
    source      = "${path.root}/ops_agent_config.yaml"
    destination = "/tmp/velda-install/ops_agent_config.yaml"
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
      "sudo cp /tmp/velda-install/velda /bin/velda",
      "sudo cp /tmp/velda-install/velda-apiserver.service /usr/lib/systemd/system/velda-apiserver.service",
      "sudo systemctl daemon-reload",
      "sudo systemctl enable velda-apiserver",
    ]
  }
  post-processor "manifest" {
    output = "controller.json"
  }
}
