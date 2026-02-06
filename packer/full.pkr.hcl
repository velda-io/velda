# Unified image for velda.
# This is used for AWS Marketplace release as single AMI image.
build {
  name = "velda"

  source "amazon-ebs.velda" {
    ami_description = "AMI for Velda ${var.version}"
    ami_name        = "velda-${var.version}"
    tags = {
      Name    = "velda-${var.version}"
      Version = var.version
      App     = "velda"
    }
    instance_type = "m4.2xlarge"
  }

  source "azure-arm.velda" {
    managed_image_name = "velda-${local.version_sanitized}"

    shared_image_gallery_destination {
      subscription         = var.azure_subscription_id
      resource_group       = var.azure_shared_image_gallery_resource_group
      gallery_name         = var.azure_shared_image_gallery
      storage_account_type = "Premium_LRS"
      image_name           = "velda"
      image_version        = var.version
    }

    vm_size = "Standard_D2as_v7"
  }


  provisioner "shell" {
    inline = [
      "sudo apt-get update",
      "sudo apt-get install -y docker.io zfsutils-linux nfs-kernel-server nfs-common unzip postgresql-client jq --no-install-recommends",
      "mkdir -p /tmp/velda-install/extra_files",
    ]
  }

  provisioner "file" {
    source      = "bin/velda-${var.version}-linux-amd64"
    destination = "/tmp/velda-install/velda"
  }
  provisioner "file" {
    source      = "${path.root}/scripts/velda-apiserver.service"
    destination = "/tmp/velda-install/velda-apiserver.service"
  }
  provisioner "file" {
    source      = "${path.root}/scripts/velda-agent.service"
    destination = "/tmp/velda-install/velda-agent.service"
  }
  provisioner "file" {
    source      = "${path.root}/scripts/nvidia-init.service"
    destination = "/tmp/velda-install/nvidia-init.service"
  }
  provisioner "shell" {
    only = ["googlecompute.velda-controller"]
    inline = [
      "curl -sSO https://dl.google.com/cloudagents/add-google-cloud-ops-agent-repo.sh",
      "sudo bash add-google-cloud-ops-agent-repo.sh --also-install",
      "rm -f add-google-cloud-ops-agent-repo.sh"
    ]
  }
  provisioner "shell" {
    inline = [
      "sudo cp /tmp/velda-install/velda /usr/bin/velda",
      "sudo chmod +x /usr/bin/velda",
      "sudo cp /tmp/velda-install/nvidia-init.service /usr/lib/systemd/system/nvidia-init.service",
      "sudo cp /tmp/velda-install/velda-agent.service /usr/lib/systemd/system/velda-agent.service",
      "sudo cp /tmp/velda-install/velda-apiserver.service /usr/lib/systemd/system/velda-apiserver.service",
      "sudo systemctl daemon-reload",
      "sudo systemctl enable nvidia-init",
      "sudo systemctl enable velda-agent",
      "rm /tmp/velda-install -rf",
      "sudo rm -f ~/.ssh/known_hosts ~/.ssh/authorized_keys /root/.ssh/known_hosts /root/.ssh/authorized_keys",
    ]
  }

  // Initialize the forwarding user
  provisioner "shell" {
    inline = [
      "sudo useradd -m -s /bin/bash velda",
      <<-EOT
      cat << EOF | sudo tee /etc/ssh/sshd_config.d/velda.conf > /dev/null
      Match User velda
        AcceptEnv VELDA_INSTANCE
        ForceCommand /usr/bin/velda run --ssh --
      EOF
      EOT
      ,
      "sudo -u velda velda init --broker localhost:50051",
      "sudo velda init --broker localhost:50051",
      "sudo mkdir -p /home/velda/.ssh",
      "sudo chown velda:velda /home/velda/.ssh",
      "sudo chmod 700 /home/velda/.ssh",
      "sudo touch /home/velda/.ssh/authorized_keys",
      "sudo chown velda:velda /home/velda/.ssh/authorized_keys",
      "sudo chmod 600 /home/velda/.ssh/authorized_keys",
    ]
  }
  // Make boot faster
  provisioner "shell" {
    inline = [
      // Mask unnecessary services.
      "sudo systemctl mask getty@tty1.service",
      "sudo systemctl mask man-db.service apt-daily.service apt-daily-upgrade.service",
      "sudo systemctl mask snapd.service snapd.seeded.service",
      // Unattended upgrader may upgrade the kernel and break the nvidia driver.
      "sudo systemctl mask unattended-upgrades.service",
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
      "echo Installing nvidia driver for kernel $(uname -r)",
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
      "(cd nvidia_driver-linux-x86_64-${var.driver_version}-archive/kernel && make -j $(nproc) && sudo make -j $(nproc) modules_install && sudo depmod -a)",
      "rm -rf nvidia_driver-linux-x86_64-${var.driver_version}-archive*",
      "echo nvidia driver installation completed",
    ]
  }
}
