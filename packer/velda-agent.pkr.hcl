build {
  name = "agent"

  source "amazon-ebs.velda" {
    ami_description = "AMI for Velda Agent ${var.version}"
    ami_name        = "velda-agent-${var.version}"
    tags = {
      Name = "velda-agent"
    }
    instance_type = "m4.2xlarge"
  }
  source "googlecompute.velda" {
    image_name        = "velda-agent-${local.version_sanitized}"
    image_description = "Image for Velda Agent"
    machine_type      = "e2-standard-4"
    image_family      = "velda-agent"
    labels = {
      name = "velda-agent"
    }
  }

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
      "sudo apt-get install -y nfs-common mdadm --no-install-recommends",
      "mkdir -p /tmp/velda-install/extra_files"
    ]
  }

  provisioner "file" {
    source      = local.binary_path
    destination = "/tmp/velda-install/client"
  }

  provisioner "file" {
    only        = ["googlecompute.velda"]
    source      = "${path.root}/ops_agent_config.yaml"
    destination = "/tmp/velda-install/ops_agent_config.yaml"
  }

  provisioner "file" {
    only        = ["amazon-ebs.velda", "azure-arm.velda"]
    source      = "${path.root}/scripts/velda-agent.service"
    destination = "/tmp/velda-install/velda-agent.service"
  }
  provisioner "file" {
    # For GCP, the agent is started by cloud-init script.
    only        = ["googlecompute.velda"]
    source      = "${path.root}/scripts/velda-agent-explicitstart.service"
    destination = "/tmp/velda-install/velda-agent.service"
  }
  provisioner "file" {
    source      = "${path.root}/scripts/nvidia-init.service"
    destination = "/tmp/velda-install/nvidia-init.service"
  }

  provisioner "file" {
    sources     = var.agent_extra_files
    destination = "/tmp/velda-install/extra_files/"
  }

  provisioner "shell" {
    only = ["googlecompute.velda"]
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
      "sudo cp /tmp/velda-install/client /usr/bin/velda",
      "sudo chmod +x /usr/bin/velda",
      "sudo cp /tmp/velda-install/nvidia-init.service /usr/lib/systemd/system/nvidia-init.service",
      "sudo cp /tmp/velda-install/velda-agent.service /usr/lib/systemd/system/velda-agent.service",
      "sudo cp -rv --preserve=mode /tmp/velda-install/extra_files/. /",
      "sudo rm -rf /tmp/velda-install",
      "sudo systemctl daemon-reload",
      "sudo systemctl enable nvidia-init",
      "sudo systemctl enable velda-agent",
    ]
  }
}

build {
  name = "agent"
  source "docker.velda" {
    changes = [
      "ENTRYPOINT [\"/velda\", \"agent\", \"daemon\"]",
    ]
  }

  provisioner "shell" {
    inline = [
      "apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y nfs-common --no-install-recommends",
    ]
  }

  provisioner "file" {
    source      = local.binary_path
    destination = "/velda"
  }

  post-processors {
    post-processor "docker-tag" {
      repository = "veldaio/agent"
      tags       = [var.version]
    }
    post-processor "docker-push" {
      login          = true
      login_username = var.docker_username
      login_password = var.docker_password
    }
  }
}