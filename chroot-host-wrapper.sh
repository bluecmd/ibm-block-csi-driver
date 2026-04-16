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

# If the command is not found on the host, fall back to the container-bundled
# binary. This handles minimal host OSes like Talos Linux that may not ship
# utilities such as blkid, sg_map, lsblk, etc. The CSI node pod has host /dev
# mounted, so container binaries can access device nodes directly.
if [ -z "${RESOLVED}" ]; then
    CONTAINER_BIN=""
    for d in /usr/sbin /usr/bin /sbin /bin; do
        if [ -x "${d}/${ME}" ]; then
            CONTAINER_BIN="${d}/${ME}"
            break
        fi
    done
    if [ -n "${CONTAINER_BIN}" ]; then
        # Commands that operate on the host filesystem (mount, umount, mkfs,
        # fsck, etc.) must run via chroot so they see host paths and devices.
        # Talos uses musl libc while the container uses glibc, so we copy
        # the binary, its libraries, and the glibc dynamic linker to the
        # host and invoke via the linker explicitly.
        case "${ME}" in
            mount|umount|mkfs.*|fsck|fsck.*|resize2fs|xfs_growfs|blockdev|blkid|lsblk)
                CSI_LIB="${DIR}/var/tmp/.csi-lib"
                mkdir -p "${CSI_LIB}"
                cp -f "${CONTAINER_BIN}" "${CSI_LIB}/${ME}"
                # Copy the glibc dynamic linker and all shared libraries
                cp -n /lib64/ld-linux-x86-64.so.2 "${CSI_LIB}/" 2>/dev/null
                ldd "${CONTAINER_BIN}" 2>/dev/null | awk '/=>/ {print $3}' | while read -r lib; do
                    [ -f "${lib}" ] && cp -n "${lib}" "${CSI_LIB}/"
                done
                exec chroot "${DIR}" "/var/tmp/.csi-lib/ld-linux-x86-64.so.2" \
                    --library-path "/var/tmp/.csi-lib" "/var/tmp/.csi-lib/${ME}" "${@:1}"
                ;;
        esac
        exec "${CONTAINER_BIN}" "${@:1}"
    fi
    echo "Could not find ${ME} on host or in container" >&2
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
        NSENTER="nsenter --mount=${DIR}/proc/${MPATHD_PID}/ns/mnt"
        # The standalone multipath binary cannot access udev inside the
        # container namespace. Redirect calls to multipathd reconfigure
        # which works through the running daemon's udev connection.
        if [ "${ME}" = "multipath" ] && [ $# -eq 0 ]; then
            MULTIPATHD_BIN="${RESOLVED%multipath}multipathd"
            exec env -i PATH="${HOST_PATH}" ${NSENTER} -- "${MULTIPATHD_BIN}" reconfigure
        fi
        exec env -i PATH="${HOST_PATH}" ${NSENTER} -- "${RESOLVED}" "${@:1}"
    fi
    # Fall through to chroot if multipathd is not running as a container.
fi

exec env -i PATH="${HOST_PATH}" chroot "${DIR}" "${RESOLVED}" "${@:1}"

