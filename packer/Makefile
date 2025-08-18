VERSION ?= dev-$(shell date +%Y%m%d-%H%M%S)

all:
	packer build -var="version=${VERSION}" .

check:
	packer validate -var="version=${VERSION}" .

controller-gcp:
	packer build -var="version=${VERSION}" -only=googlecompute.velda-controller .

controller-aws:
	packer build -var="version=${VERSION}" -only=amazon-ebs.velda-controller .

agent-gcp:
	packer build -var="version=${VERSION}" -only=googlecompute.velda-agent --force .

agent-aws:
	packer build -var="version=${VERSION}" -only=amazon-ebs.velda-agent .

update_permission:
	gcloud compute images add-iam-policy-binding projects/velda-oss/global/images/velda-controller-${VERSION} \
		--member='allAuthenticatedUsers' \
		--role='roles/compute.imageUser'
	gcloud compute images add-iam-policy-binding projects/velda-oss/global/images/velda-agent-${VERSION} \
		--member='allAuthenticatedUsers' \
		--role='roles/compute.imageUser'
