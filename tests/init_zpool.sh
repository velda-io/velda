#!/bin/bash
set -e

scriptDir=$(dirname "$0")
zpool_root=$1
if [ -z "$zpool_root" ]; then
  echo "Usage: $0 <zpool_root>"
  exit 1
fi

function export_docker() {
  cat ${scriptDir}/dockers/$1.Dockerfile | docker build -t veldatests/$1 -
  container=$(docker create veldatests/$1)
  zfs destroy -r $zpool_root/$1 2> /dev/null || true
  zfs create $zpool_root/seed/$1
  docker export $container | tar -x -C /$zpool_root/seed/$1
  zfs snapshot $zpool_root/seed/$1@image
  docker rm -f $container >/dev/null
}

zfs create $zpool_root/seed
export_docker ubuntu
export_docker ubuntu-docker