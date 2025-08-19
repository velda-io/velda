# Initialize user
install_sudo() {
    $(which -s apt-get) && apt-get update && apt-get install -y sudo && return 0
    $(which -s yum) && yum install -y sudo && return 0
    $(which -s dnf) && dnf install -y sudo && return 0
}

$(which -s sudo) || install_sudo
useradd user -m -s /bin/bash
passwd -d user
mkdir -p /etc/sudoers.d
echo "user ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/user
usermod -aG sudo user

ln -s /run/velda/velda /usr/bin/velda
ln -s /run/velda/velda /usr/bin/vbatch
ln -s /run/velda/velda /usr/bin/vrun
ln -s /run/velda/velda /sbin/mount.host