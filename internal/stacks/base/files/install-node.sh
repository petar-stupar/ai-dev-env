#!/bin/sh
# install-node.sh NN: install the latest Node.js NN.x from nodejs.org into
# /opt/node/NN, checked against its SHASUMS256.txt, plus nodeNN/npmNN/npxNN
# wrappers in /usr/local/bin. Wrappers rather than symlinks: npm's shebang is
# `/usr/bin/env node`, which would otherwise pick the default node.
set -eu

major="${1:?usage: install-node.sh MAJOR}"
case "$major" in
    *[!0-9]*|'') echo "install-node.sh: major version must be a number: $major" >&2; exit 2 ;;
esac

case "${TARGETARCH:-$(uname -m)}" in
    amd64|x86_64) arch=x64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) echo "install-node.sh: unsupported architecture: ${TARGETARCH:-$(uname -m)}" >&2; exit 1 ;;
esac

base="https://nodejs.org/dist/latest-v$major.x"
dest="/opt/node/$major"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL -o "$tmp/SHASUMS256.txt" "$base/SHASUMS256.txt"
# Lines are "<sha256>  <file>". The URL has no version, so the exact
# filename comes from the list; match whole fields, never substrings.
tarball="$(awk -v re="^node-v[0-9.]+-linux-${arch}[.]tar[.]xz\$" '$2 ~ re { print $2; exit }' "$tmp/SHASUMS256.txt")"
[ -n "$tarball" ] || { echo "install-node.sh: no linux-$arch tarball in $base" >&2; exit 1; }
sum="$(awk -v f="$tarball" '$2 == f { print $1; exit }' "$tmp/SHASUMS256.txt")"

curl -fsSL -o "$tmp/$tarball" "$base/$tarball"
(cd "$tmp" && echo "$sum  $tarball" | sha256sum -c -)

rm -rf "$dest"
mkdir -p "$dest"
tar -xJf "$tmp/$tarball" -C "$dest" --strip-components=1 --no-same-owner
# The tarball's npm lags the npm release line; take the current one, as the
# old nodesource-based image did.
PATH="$dest/bin:$PATH" "$dest/bin/npm" install -g npm@latest >/dev/null
# Owned by agent so `npm i -g` works without sudo.
if id agent >/dev/null 2>&1; then
    chown -R agent:agent "$dest"
fi

for tool in node npm npx; do
    cat >"/usr/local/bin/$tool$major" <<WRAPPER
#!/bin/sh
PATH=$dest/bin:\$PATH exec $dest/bin/$tool "\$@"
WRAPPER
    chmod 0755 "/usr/local/bin/$tool$major"
done

echo "node$major: $("/usr/local/bin/node$major" --version), npm $("/usr/local/bin/npm$major" --version)"
