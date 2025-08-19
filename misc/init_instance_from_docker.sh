#!/bin/bash
# Initialize an instance from a docker image.
VELDA=${VELDA:-velda}
set -e
usage() {
  echo "Usage: $0 [docker-image] [velda-instance]"
}
SCRIPTDIR=$(dirname "$0")
DOCKER_NAME=$1
INST_NAME=$2
[ -z "$DOCKER_NAME" ] && {
  usage
  exit 1
}
container=$(docker create $DOCKER_NAME)
outputd=$(sudo mktemp -d)
trap "sudo rm -rf ${outputd}" EXIT

docker export $container | sudo tar -x -C ${outputd}
echo -e "\e[32mExtracted docker image to ${outputd}\e[0m"
echo "Copying files to instance ${INST_NAME}..."
sudo ${VELDA} scp --preserve -u root -s t1 -r ${outputd}/. $INST_NAME:/
echo -e "\e[32mCompleted initialization of instance from docker image.\e[0m"
instance_param=" "
if [ -n "$INST_NAME" ]; then
  instance_param=" --instance ${INST_NAME}"
fi

echo "Installing Velda in the instance..."
cat ${SCRIPTDIR}/init_sandbox.sh | ${VELDA} run${instance_param} -u root sh

echo -e "\e[32mVelda installed in the instance.\e[0m"
echo "Use \`${VELDA} run${instance_param}\` to connect with the instance."