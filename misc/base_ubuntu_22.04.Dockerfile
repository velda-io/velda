# IMAGE: veldaio/base-ubuntu-oss:22.04
FROM ubuntu:22.04
# Install common packages
RUN apt update && apt install less vim curl wget git man sudo bash-completion ca-certificates psmisc docker.io python3 virtualenv -y --no-install-recommends
RUN yes | unminimize

# Initialize user, setup velda
RUN useradd user -m -s /bin/bash && passwd -d user && usermod -aG sudo user && \
    echo "user ALL=(ALL) NOPASSWD: ALL" > /etc/sudoers.d/user

RUN ln -s /run/velda/velda /usr/bin/velda && \
    ln -s /run/velda/velda /usr/bin/vbatch && \
    ln -s /run/velda/velda /usr/bin/vrun && \
    ln -s /run/velda/velda /sbin/mount.host
