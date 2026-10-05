#!/bin/bash
# Installed as /usr/local/sbin/mount, ahead of the real mount on sudo's
# secure_path, and the only mount `agent` may run as root without a password.
# The 9P tools (terminalfs, dotnetdoc) run as agent and call `sudo mount`
# themselves, so that one call has to stay open; everything it could be
# turned into is refused here.
#
# Accepted: mount -t 9p -o <options> 127.0.0.1 <dir under /home/agent/mnt>
# with TCP transport and a plain option list. nosuid and nodev are added: the
# server on the other end runs as agent and decides what the files look like,
# and a set-uid root file served from there would be a way to root.
#
# For any other mount, root can call /usr/bin/mount directly.
set -euo pipefail
PATH=/usr/sbin:/usr/bin:/sbin:/bin

# /usr/local/sbin is on everyone's PATH, so this is also what a plain `mount`
# finds. Only a call that arrives as root is the one sudo let through; anyone
# else gets the real mount with their own (lack of) privilege, and listing
# the mount table changes nothing whoever asks. terminalfs does both.
if [ "$(id -u)" != 0 ] || [ $# -eq 0 ]; then
    exec /usr/bin/mount "$@"
fi

root=/home/agent/mnt
die() { echo "mount (aide): $*" >&2; exit 1; }

type='' opts='' args=()
while [ $# -gt 0 ]; do
    case "$1" in
        -t) [ $# -ge 2 ] || die "-t needs a value"; type="$2"; shift 2 ;;
        -o) [ $# -ge 2 ] || die "-o needs a value"; opts="${opts:+$opts,}$2"; shift 2 ;;
        -*) die "option $1 is not allowed" ;;
        *) args+=("$1"); shift ;;
    esac
done

[ "$type" = 9p ] || die "only 9p mounts are allowed here; root can use /usr/bin/mount"
[ "${#args[@]}" -eq 2 ] || die "expected <server> <mountpoint>"
[ "${args[0]}" = 127.0.0.1 ] || die "the server must be 127.0.0.1"
target="${args[1]}"

tcp=0 port=0
IFS=, read -r -a list <<<"$opts"
for o in "${list[@]}"; do
    case "$o" in
        trans=tcp) tcp=1 ;;
        port=*) [[ "${o#port=}" =~ ^[0-9]{1,5}$ ]] || die "bad option $o"; port=1 ;;
        msize=*) [[ "${o#msize=}" =~ ^[0-9]{1,9}$ ]] || die "bad option $o" ;;
        dfltuid=*|dfltgid=*) [[ "${o#*=}" =~ ^[0-9]{1,9}$ ]] || die "bad option $o" ;;
        version=9p2000|version=9p2000.u|version=9p2000.L) ;;
        cache=none|cache=loose|cache=mmap|cache=readahead|cache=fscache) ;;
        uname=*) [[ "${o#uname=}" =~ ^[A-Za-z0-9_-]{1,32}$ ]] || die "bad option $o" ;;
        access=any|access=user|access=client) ;;
        ro|rw|noatime|nosuid|nodev|noexec) ;;
        *) die "mount option '$o' is not allowed" ;;
    esac
done
if [ "$tcp" != 1 ] || [ "$port" != 1 ]; then
    die "trans=tcp and port=<n> are required"
fi

# The mountpoint is checked through an open descriptor and mounted through
# that same descriptor, so swapping a path component for a symlink between
# the check and the mount gains nothing.
if [ ! -d "$target" ] || [ -L "$target" ]; then
    die "$target is not a directory"
fi
exec 9<"$target"
real="$(readlink /proc/$$/fd/9)"
case "$real" in
    "$root"/*) ;;
    *) die "$target is not under $root" ;;
esac

exec /usr/bin/mount --no-canonicalize -t 9p -o "$opts,nosuid,nodev" 127.0.0.1 "/proc/self/fd/9"
