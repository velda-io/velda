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

update_permission:
# Only update permission if VERSION doesn't contain dev or teset
ifneq (,$(filter dev test,$(word 1,$(subst -, ,$(VERSION)))))
else
	gcloud compute images add-iam-policy-binding projects/velda-oss/global/images/velda-controller-$(subst .,-,$(VERSION)) \
		--member='allAuthenticatedUsers' \
		--role='roles/compute.imageUser'
	gcloud compute images add-iam-policy-binding projects/velda-oss/global/images/velda-agent-$(subst .,-,$(VERSION)) \
		--member='allAuthenticatedUsers' \
		--role='roles/compute.imageUser'
endif

init:
	packer init .
