VERSION ?= dev-$(shell date +%Y%m%d-%H%M%S)

all: init
	packer build -var="version=${VERSION}" .
	${MAKE} update_permission

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

update_permission_controller:
	gcloud compute images add-iam-policy-binding projects/velda-oss/global/images/velda-controller-$(subst .,-,$(VERSION)) \
		--member='allAuthenticatedUsers' \
		--role='roles/compute.imageUser'

update_permission_agent:
	gcloud compute images add-iam-policy-binding projects/velda-oss/global/images/velda-agent-$(subst .,-,$(VERSION)) \
		--member='allAuthenticatedUsers' \
		--role='roles/compute.imageUser'

init:
	packer init .
