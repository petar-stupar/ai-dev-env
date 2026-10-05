#!/bin/bash
# Installed as /usr/local/sbin/umount: the only umount `agent` may run as root
# without a password. It detaches a 9P mount under /home/agent/mnt and nothing
# else. See aide-mount.sh.
set -euo pipefail
PATH=/usr/sbin:/usr/bin:/sbin:/bin

# Not root: this is a plain `umount` that found the wrapper on PATH, and the
# real one decides with the caller's own privilege.
if [ "$(id -u)" != 0 ]; then
    exec /usr/bin/umount "$@"
fi

root=/home/agent/mnt
die() { echo "umount (aide): $*" >&2; exit 1; }

flags=() args=()
while [ $# -gt 0 ]; do
    case "$1" in
        -l|-f) flags+=("$1"); shift ;;
        -*) die "option $1 is not allowed" ;;
        *) args+=("$1"); shift ;;
    esac
done
[ "${#args[@]}" -eq 1 ] || die "expected one mountpoint"
target="${args[0]%/}"

case "$target" in
    "$root"/*) ;;
    *) die "$target is not under $root" ;;
esac
case "$target/" in
    */../*|*/./*|*//*) die "$target is not a plain path" ;;
esac

# It must be a 9P mount exactly there; /proc/mounts writes a space as \040.
found=0
while read -r _ where fstype _; do
    where="${where//\\040/ }"
    if [ "$where" = "$target" ] && [ "$fstype" = 9p ]; then
        found=1
    fi
done </proc/self/mounts
[ "$found" = 1 ] || die "$target is not a 9p mount"

exec /usr/bin/umount --no-canonicalize "${flags[@]}" -- "$target"
