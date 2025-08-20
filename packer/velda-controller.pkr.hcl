build {
  name = "controller"

  source "amazon-ebs.velda" {
    ami_description = "AMI for Velda controller ${var.version}"
    ami_name        = "velda-controller-${var.version}"
    tags = {
      Name = "velda-controller"
    }
    instance_type = "t2.small"
  }
  source "googlecompute.velda" {
    image_name        = "velda-controller-${local.version_sanitized}"
    image_description = "Image for Velda controller"
    machine_type      = "e2-micro"
    image_family      = "velda-controller"
    labels = {
      name = "velda-controller"
    }
  }

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
