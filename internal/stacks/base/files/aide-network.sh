#!/bin/bash
# The namespace's network allowlist, inside the container. Root only.
#
#   aide-network.sh start          write the filter, start the proxy, close
#                                  every other way out; exits non-zero if the
#                                  container would be left with open network
#   aide-network.sh stop           undo what start left in the filesystem (a
#                                  snapshot may carry it into an open namespace)
#   aide-network.sh allow [HOST]...  replace the namespace's own hosts (aide
#                                  runs this through docker exec) and reload
#
# Hosts come from two lists: /etc/aide/allow-hosts, what the stacks of this
# image need, and /etc/aide/proxy/allow-hosts.user, what the namespace added.
# An entry is a host name, or *.name for its subdomains.
set -euo pipefail
PATH=/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

dir=/etc/aide/proxy
ssh_conf=/etc/ssh/ssh_config.d/90-aide-proxy.conf
conf="$dir/tinyproxy.conf"
filter="$dir/filter"
user_hosts="$dir/allow-hosts.user"
port=3128

log() { printf '[network] %s\n' "$*"; }
die() { printf '[network] ERROR: %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "must run as root"

host_re='^(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$'

# write_filter: one anchored pattern per host, so "github.com" does not also
# let "evilgithub.com" or "github.com.evil.net" through.
write_filter() {
    local tmp h n=0
    tmp="$(mktemp "$dir/filter.XXXXXX")"
    while IFS= read -r h; do
        [ -n "$h" ] || continue
        [[ "$h" =~ $host_re ]] || { log "ignoring invalid host '$h'"; continue; }
        if [[ "$h" == \*.* ]]; then
            h="${h#\*.}"
            printf '\\.%s$\n' "${h//./\\.}" >>"$tmp"
        else
            printf '^%s$\n' "${h//./\\.}" >>"$tmp"
        fi
        n=$((n + 1))
    done < <(cat /etc/aide/allow-hosts "$user_hosts" 2>/dev/null | sort -u)
    chmod 0644 "$tmp"
    mv -f "$tmp" "$filter"
    log "$n host(s) allowed"
}

# ipt: iptables for both address families. A family the kernel does not have
# is not an error: nothing can leave over it either.
ipt4() { iptables -w "$@"; }
ipt6() { ip6tables -w "$@" 2>/dev/null || true; }

install_rules() {
    local uid
    uid="$(id -u tinyproxy)"
    for ipt in ipt4 ipt6; do
        $ipt -F OUTPUT
        $ipt -A OUTPUT -o lo -j ACCEPT
        # Replies on connections that came in (code-server behind its
        # published port) are not new traffic.
        $ipt -A OUTPUT -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
        $ipt -A OUTPUT -m owner --uid-owner "$uid" -j ACCEPT
        $ipt -A OUTPUT -j REJECT
    done
    # The last rule of the chain must be the reject, or the network is open.
    iptables -w -S OUTPUT | tail -n 1 | grep -q -- '-j REJECT' \
        || die "the firewall rules are not in place"
}

case "${1:-}" in
    start)
        install -d -m 0755 "$dir"
        install -d -o tinyproxy -g tinyproxy -m 0750 /var/log/aide-proxy /run/aide-proxy
        [ -e "$user_hosts" ] || : >"$user_hosts"
        write_filter
        # ssh knows nothing of http_proxy, so it is told here to reach its
        # hosts through the proxy; git over ssh then works for allowed hosts.
        install -d -m 0755 "$(dirname "$ssh_conf")"
        printf '%s\n' \
            '# Written by aide-network.sh: the network of this namespace is an allowlist.' \
            'Host * !localhost !127.0.0.1 !::1' \
            "    ProxyCommand /usr/bin/nc -X connect -x 127.0.0.1:$port %h %p" >"$ssh_conf"
        chmod 0644 "$ssh_conf"
        # The rules go in first: if the proxy then fails to start, nothing
        # gets out, which is the safe way round.
        install_rules
        rm -f /run/aide-proxy/tinyproxy.pid
        tinyproxy -c "$conf" || die "tinyproxy did not start"
        for _ in $(seq 1 50); do
            if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
                log "proxy listening on 127.0.0.1:$port; all other outgoing traffic is refused"
                exit 0
            fi
            sleep 0.1
        done
        die "tinyproxy is not listening on 127.0.0.1:$port"
        ;;
    stop)
        rm -f "$ssh_conf"
        ;;
    allow)
        shift
        install -d -m 0755 "$dir"
        tmp="$(mktemp "$dir/hosts.XXXXXX")"
        for h in "$@"; do
            [[ "$h" =~ $host_re ]] || die "invalid host '$h'"
            printf '%s\n' "$h" >>"$tmp"
        done
        chmod 0644 "$tmp"
        mv -f "$tmp" "$user_hosts"
        write_filter
        # tinyproxy rereads its filter on SIGUSR1; not running yet is fine,
        # start reads the same files.
        pkill -USR1 -x tinyproxy 2>/dev/null || true
        ;;
    *)
        die "usage: aide-network.sh start | stop | allow [HOST]..."
        ;;
esac
