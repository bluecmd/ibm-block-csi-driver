# IBM block storage CSI driver (fork)

> **This is a fork.** This repository is [bluecmd](https://github.com/bluecmd)'s
> fork of [IBM/ibm-block-csi-driver](https://github.com/IBM/ibm-block-csi-driver).
> It is not affiliated with or supported by IBM. For the upstream driver, its
> documentation and its releases, use the upstream repository. See
> [Fork notes](#fork-notes) for what is different here.

The Container Storage Interface (CSI) Driver for IBM block storage systems enables container orchestrators such as Kubernetes to manage the life cycle of persistent storage.

For compatibility, prerequisites, release notes, and other user information, see [IBM block storage CSI driver documentation](https://www.ibm.com/docs/en/stg-block-csi-driver).

## Fork notes

### What is different from upstream

* **Talos Linux hosts.** `chroot-host-wrapper.sh` no longer depends on
  `/usr/bin/env` existing on the host and resolves host binaries itself.
  `multipath`/`multipathd` calls enter the mount namespace of the running
  `multipathd` (Talos runs it as a system extension with a writable rootfs)
  instead of chrooting into the immutable host rootfs.

### Planned

* **Linux ARM64.** Build and publish `linux/arm64` images for the node,
  controller and host definer alongside `linux/amd64`.
* **Windows nodes.** A node plugin that can run on Windows worker nodes.

### Branches and tags

* `main` is the working branch. It is upstream's current release branch
  (`release-1.14.0` at the time of writing) plus the fork's changes.
  Upstream branches are mirrored unchanged under their upstream names.
* Fork releases are tagged `vX.Y.Z-fork.N`, where `X.Y.Z` is the upstream
  version the release is based on.

### Images

Images are built by GitHub Actions and published to the GitHub container
registry:

* `ghcr.io/bluecmd/ibm-block-csi-driver-controller`
* `ghcr.io/bluecmd/ibm-block-csi-driver-node`
* `ghcr.io/bluecmd/ibm-block-csi-host-definer`

Every push to `main` publishes the `main` tag and a `sha-<commit>` tag. Git
tags publish an image tag of the same name. The image names mirror upstream's
`quay.io/ibmcsiblock/*` images so they can be dropped into an `IBMBlockCSI`
custom resource's `repository` fields.

### Continuous integration

`.github/workflows/ci.yaml` runs the same checks as upstream's Jenkins
pipeline (Go unit tests, Python lint and unit tests, manifest validation) on
every pull request and push to `main`. `.github/workflows/images.yaml` builds
the images and pushes them on `main` and on tags.

## Licensing

Copyright 2025 IBM Corp.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.

