# What's new in 1.13.0

IBM® block storage CSI driver 1.13.0 adds support for:

- IBM Storage Virtualize® partitions, including Policy Based High Availability (PBHA) on partitions, over host FC only.
- IBM Storage Virtualize® - option to define secret-specific port set

**General availability date:** December 2025

## Miscellaneous resolved issues

For information about the resolved issues in version 1.13.0, see [1.13.0](changelog_1.13.0.md).

In this version a persistency over upgrade for configuration change has been added. If you want your configuration to be saved over upgrade to this version, create a configmap prior to upgrading to this version! On the next upgrade you will already have this configmap and will not have to create it again.{: attention}

More info regarding this configmap in [Configuring the host definer](content/configuration/configuring_hostdefiner.md)
