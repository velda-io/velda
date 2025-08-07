
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