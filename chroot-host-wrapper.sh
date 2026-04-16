#!/usr/bin/env bash

ME=$(basename "$0")

DIR="/host"   # The CSI node daemonset mount the / of the host into /host inside the container.
if [ ! -d "${DIR}" ]; then
    echo "Could not find docker engine host's filesystem at expected location: ${DIR}"
    exit 1
fi

# sg_map and sg_inq from sg3_utils only need access to /dev, which is
# available inside the container. Run them directly to avoid requiring
# sg3_utils on the host.
if [ "${ME}" = "sg_map" ] || [ "${ME}" = "sg_inq" ]; then
    CONTAINER_BIN=$(command -v "${ME}" 2>/dev/null)
    if [ -n "${CONTAINER_BIN}" ]; then
        exec "${CONTAINER_BIN}" "${@:1}"
    fi
fi

# Resolve the command path by searching the host filesystem from inside the
# container. This avoids requiring /usr/bin/env to exist on the host, which is
# not the case on minimal host OSes like Talos Linux.
HOST_PATH="/usr/local/sbin:/usr/local/bin:/sbin:/bin:/usr/bin:/usr/sbin"
RESOLVED=""
IFS=':' read -ra DIRS <<< "${HOST_PATH}"
for d in "${DIRS[@]}"; do
    if [ -x "${DIR}${d}/${ME}" ]; then
        RESOLVED="${d}/${ME}"
        break
    fi
done

if [ -z "${RESOLVED}" ]; then
    echo "Could not find ${ME} in host filesystem (searched: ${HOST_PATH})" >&2
    exit 1
fi

# On immutable-rootfs OSes like Talos Linux, multipath/multipathd run inside
# an extension service container with a writable rootfs. Enter that
# container's mount namespace instead of chrooting to the host, where
# /etc/multipath cannot be created on the read-only rootfs.
if [ "${ME}" = "multipath" ] || [ "${ME}" = "multipathd" ]; then
    MPATHD_PID=""
    for pid in "${DIR}"/proc/[0-9]*; do
        if [ "$(cat "${pid}/comm" 2>/dev/null)" = "multipathd" ]; then
            MPATHD_PID=$(basename "${pid}")
            break
        fi
    done
    if [ -n "${MPATHD_PID}" ]; then
        exec env -i PATH="${HOST_PATH}" nsenter --mount="${DIR}/proc/${MPATHD_PID}/ns/mnt" -- "${RESOLVED}" "${@:1}"
    fi
    # Fall through to chroot if multipathd is not running as a container.
fi

exec env -i PATH="${HOST_PATH}" chroot "${DIR}" "${RESOLVED}" "${@:1}"

