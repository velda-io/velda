FROM ubuntu:24.04

RUN apt update && apt install -y sudo
# Initialize user, setup velda
RUN useradd user -m -s /bin/bash && passwd -d user && usermod -aG sudo user && \
    echo "user ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/velda

RUN ln -s /run/velda/velda /usr/bin/velda && \
    ln -s /run/velda/velda /usr/bin/vbatch && \
    ln -s /run/velda/velda /usr/bin/vrun && \
    ln -s /run/velda/velda /sbin/mount.host
