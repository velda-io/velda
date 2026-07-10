# GitHub Actions Integration

Velda can run GitHub Actions jobs on Velda-managed compute by using GitHub's self-hosted runner model.

## Overview

When a workflow job uses `runs-on` labels that match Velda's runner labels, Velda will provision or select compute and run the job there.

Common labels:

- `velda`: required marker label for Velda-managed jobs.
- `instance=<name>`: run against a specific template instance.
- `pool=<name>`: choose the Velda pool.
- `region=<name>`: route to a specific region.
- `snapshot=<name>`: use a specific snapshot from the template instance.

Example:

```yaml
jobs:
  build:
    runs-on: ["velda", "instance=ci-template", "pool=shell"]
    steps:
      - uses: actions/checkout@v4
      - run: go test ./...
```

## Installation Flow

1. In Velda, open Settings and choose GitHub Actions.
2. Click Connect GitHub and install the Velda GitHub App in your GitHub org or user account.
3. After GitHub redirects back, explicitly confirm the installation in Velda.
4. Select the target organization from the app bar if needed before confirming.

## Usage Patterns

### 1) Basic job on Velda

```yaml
jobs:
  basic:
    runs-on: ["velda", "instance=ci-template"]
    steps:
      - uses: actions/checkout@v4
      - run: vrun -P h100-1 ./gpu-test.sh
```

### 2) Pool and region override

```yaml
jobs:
  region-test:
    runs-on: ["velda", "instance=ci-template", "pool=shell", "region=aws-us-west2"]
    steps:
      - uses: actions/checkout@v4
      - run: env | sort
```
## More Examples

See the [Velda examples repository](https://github.com/velda-io/examples) for up-to-date end-to-end samples:


