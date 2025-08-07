#!/bin/bash
set -eux
mv /tmp/velda-install/apiserver /bin/velda-apiserver
mv /tmp/velda-install/velda-apiserver.service /usr/lib/systemd/system/velda-apiserver.service

systemctl daemon-reload