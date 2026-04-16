#!/usr/bin/env bash

ME=$(basename "$0")

DIR="/host"   # The CSI node daemonset mount the / of the host into /host inside the container.
if [ ! -d "${DIR}" ]; then
    echo "Could not find docker engine host's filesystem at expected location: ${DIR}"
    exit 1
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

# Ensure directories that host utilities expect to exist are present.
# On immutable-rootfs OSes like Talos Linux, these may not be pre-created.
mkdir -p "${DIR}/etc/multipath"

exec env -i PATH="${HOST_PATH}" chroot "${DIR}" "${RESOLVED}" "${@:1}"

