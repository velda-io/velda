VERSION ?= dev-$(shell date +%Y%m%d)

controller-gcp:
	packer build -var="version=${VERSION}" -only=googlecompute.velda-controller velda-controller.pkr.hcl

controller-aws:
	packer build -var="version=${VERSION}" -only=amazon-ebs.velda-controller velda-controller.pkr.hcl

agent-gcp:
	packer build -var="version=${VERSION}" -only=googlecompute.velda-agent --force velda-agent.pkr.hcl 
	gcloud compute images add-iam-policy-binding projects/velda-oss/global/images/velda-agent-${VERSION} \
		--member='allAuthenticatedUsers' \
		--role='roles/compute.imageUser'

agent-aws:
	packer build -var="version=${VERSION}" -only=amazon-ebs.velda-agent velda-agent.pkr.hcl

AMI_ID := $(shell jq -r '.builds[-1].artifact_id' base-aws.json | grep -o 'us-east-1:ami-\w*' | cut -d':' -f2)
agent-gpu-aws:
	packer build -var="version=${VERSION}" -var="source_ami=${AMI_ID}" -only=amazon-ebs.velda-agent velda-agent-gpu.pkr.hcl