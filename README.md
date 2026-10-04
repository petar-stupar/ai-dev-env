# aide

Isolated development environments for agentic work. `aide` is one Go binary that
builds a Docker image from a set of named toolchains (**stacks**), runs one
container per **namespace**, and lets you change which host directories are
mounted into it while it runs. Each container has browser-based VS Code, Claude
Code and opencode, with the agents' shell access routed through a 9P filesystem
instead of a shell tool.

It replaces the old `build.sh` / `run.sh` implementation of ai-dev-env. See
[Migrating from ai-dev-env](#migrating-from-ai-dev-env).

## Quick start

```sh
go install github.com/petar-stupar/ai-dev-env/cmd/aide@latest

aide ns new web                    # the default stacks, see Stacks below
aide ns:web build                  # slow the first time
aide ns:web mount ~/src/frontend /home/agent/workspace/frontend
aide ns:web start
```

`start` prints the URL and the password. Open the URL and enter the password.
VS Code Server is published on `127.0.0.1` only.

Sign in to the agents once per namespace:

```sh
docker exec -it -u agent aide-web bash -l
claude          # then /login
opencode auth login
```

Add or drop mounts at any time with `mount` and `umount`. On a running
namespace that restarts the container and keeps what you installed inside it.

## Concepts

| Term | Meaning |
| --- | --- |
| Namespace | A named environment: its stacks, its mounts, and the Docker objects aide creates for it. |
| Stack | A module that adds a toolchain or tool to the image: a Dockerfile fragment, an optional start hook, run-time requirements. |
| Mount | A host directory bind-mounted at a container path. |
| Base image | `aide-<ns>:base`, the result of `build`. |
| Snapshot | `aide-<ns>:snap`, the container's filesystem committed during a remount. If one exists the container runs from it rather than from the base image. |
| `.aide` file | A namespace configuration saved as text: `stack` and `mount` commands, one per line. |

Docker objects per namespace: base image `aide-<ns>:base`, snapshot `aide-<ns>:snap` (a moving tag), container `aide-<ns>`, volumes `aide-<ns>-creds` and `aide-<ns>-cache`.

Local state lives in `~/.config/aide` (or `$XDG_CONFIG_HOME/aide`), on Linux and
macOS alike:

```
~/.config/aide/
  default.aide                  seeded from the binary on first run; edit this copy
  namespaces/<ns>/state.json    stacks, mounts, image id, snapshot id, port, password
```

Namespace names are lower case letters, digits and hyphens, up to 32 characters.
The port is picked once, from 8080 up, and kept.

`aide ns:<name> state` prints a namespace as a `.aide` file:

```
aide ns:web stack base dotnet10 node24 claude opencode-filesystem
aide ns:web mount /Users/petar/src/frontend /home/agent/workspace/frontend
```

Save it with `aide ns:web state > web.aide`, replay it with
`aide ns new other web.aide`. Only `stack` and `mount` lines and `#` comments are
allowed, the namespace on each line is ignored, and host paths may start with
`~`. There is no `unstack`: edit the file and create a new namespace.

## Commands

```
aide [-v] ns new <name> [file.aide]       create a namespace; "-" reads stdin,
                                          default.aide when no file is given
aide [-v] ns clone <existing> <new> [--yes]
aide [-v] ns remove <name> [--keep-volumes] [--yes]
aide [-v] ns list                         NAME PHASE PORT MOUNTS STACKS
aide [-v] ns:<name> stack <stack>...      add stacks
aide [-v] ns:<name> build [--no-cache]
aide [-v] ns:<name> start [--yes]
aide [-v] ns:<name> stop
aide [-v] ns:<name> reset [--yes]
aide [-v] ns:<name> mount <host> <container> [<host> <container>]... [--yes]
aide [-v] ns:<name> umount <container-path>... [--yes]
aide [-v] ns:<name> state                 print the configuration as .aide text
aide [-v] ns:<name> dockerfile            print the Dockerfile build would use
aide stacks                               list the stack catalog
aide version
aide help
```

`mount` and `umount` take several pairs or paths, so switching to a new set of
repos costs one restart, not one per repo.

`clone` commits the source container, so the clone carries the work done inside
it, and copies the credentials volume, so the clone is already signed in. The
cache volume starts empty and the clone gets a new port and password. It refuses
while the source is running unless you pass `--yes`, which commits it paused.

### Command availability

| Command | No image | Built, no container | Stopped | Running |
| --- | --- | --- | --- | --- |
| `stack` | yes | yes, marks the image stale | no, `reset` first | no, `reset` first |
| `build` | yes | yes | no, `reset` first | no, `reset` first |
| `start` | no | yes | yes | no-op |
| `stop` | no-op | no-op | no-op | yes |
| `mount` / `umount` | recorded | recorded | recorded, applied on `start` | remount now |
| `reset` | no-op | no-op | yes | stops, then yes |
| `state` | yes | yes | yes | yes |

`build` is refused while a container exists because rebuilding would throw away
the snapshot. `reset` is the explicit way to accept that loss. `start` refuses
when the image was built from a different stack set than the namespace now
names ("stale image"): `reset`, then `build`.

### Flags and environment

| | |
| --- | --- |
| `--yes` | skip confirmation prompts |
| `-v` | echo every docker command line to stderr; accepted anywhere on the line |
| `AIDE_DOCKER` | the docker binary to use; `docker` by default |
| `AIDE_FLATTEN_THRESHOLD` | layer count above which a snapshot is flattened; 100 by default |
| `XDG_CONFIG_HOME` | config root; `~/.config` by default, aide uses `<root>/aide` |

aide calls the `docker` CLI, so it follows your current Docker context.

## Stacks

`aide stacks` lists the catalog. Stacks are embedded in the binary.

| Stack | What it installs | Requires | Conflicts | SYS_ADMIN |
| --- | --- | --- | --- | --- |
| `base` | Ubuntu 24.04, build tools, git, ripgrep, tmux, neovim, code-server, the `agent` user, the entrypoint | | | |
| `dotnet8` | .NET 8 SDK | base | | |
| `dotnet10` | .NET 10 SDK | base | | |
| `python3` | Python 3 with pip and venv | base | | |
| `clang` | clang, lld, lldb and clangd (LLVM 18) | base | | |
| `golang` | Go, current stable; module and build caches on the cache volume | base | | |
| `node22` | Node.js 22 and npm | base | | |
| `node24` | Node.js 24 and npm | base | | |
| `node26` | Node.js 26 and npm | base | | |
| `gh` | GitHub CLI; login kept on the credentials volume | base | | |
| `qode` | [qode](https://github.com/nqode-io/qode), nQode's AI workflow CLI, in `~/.local/bin` | base | | |
| `terminalfs` | [terminalfs](https://github.com/petar-stupar/terminalfs): shell commands as files, one 9P tree per agent session; plugins for Claude Code and opencode | base | | yes |
| `claude` | Claude Code and its VS Code extension; sign-in kept on the credentials volume | base | | |
| `opencode` | upstream opencode | base | `opencode-filesystem` | |
| `opencode-filesystem` | the 9P-filesystem build of opencode from [petar-stupar/opencode](https://github.com/petar-stupar/opencode), with no shell or web tools | base, terminalfs | `opencode` | |
| `dotnetdocfs` | [dotnetdocfs](https://github.com/petar-stupar/dotnetdocfs): .NET and NuGet API docs as files at `~/mnt/dotnetdocfs`, with its skill for both agents; suggests a dotnet stack | base | | yes |

`stack` adds dependencies for you: `aide ns:web stack opencode-filesystem` also
selects `base` and `terminalfs`. The stacks are ordered the same way whatever
order you name them in, so the build cache is shared between namespaces. A
stack that needs `SYS_ADMIN` makes the container run with `--cap-add SYS_ADMIN`;
on Linux hosts with AppArmor it also gets `--security-opt apparmor=unconfined`.

Versions are "latest at build time". There is no way to pin them from the
command line yet. `aide ns:web build --no-cache` after a `reset` picks up new
releases of the agents and tools.

### Side by side

`dotnet8` and `dotnet10` install into the same `/usr/share/dotnet`. The SDK
picks a version per project through `global.json`, as it does anywhere else.

Each `nodeNN` stack installs into `/opt/node/NN` and adds versioned commands
(`node22`, `npm22`, `npx22`, and so on) to `/usr/local/bin`. The highest
selected version is also the default `node`, `npm` and `npx` on `PATH`.

### default.aide

`aide ns new <name>` with no file applies `~/.config/aide/default.aide`. It is
seeded from the binary on first run:

```
aide ns:default stack base dotnet10 node24 python3 clang golang gh qode claude terminalfs opencode-filesystem dotnetdocfs
```

That is every stack that can coexist, one version each. `dotnet8`, `node22` and
`node26` are left out as second versions, and `opencode` conflicts with
`opencode-filesystem`. Edit the file in `~/.config/aide` to change what new
namespaces start with. Existing namespaces are not touched.

## How the agents are shaped

Both agents are pushed towards the same shape: no web access, and commands run
by writing to a filesystem rather than through a shell tool.

terminalfs gives every agent session its own tree, mounted at
`/home/agent/mnt/terminalfs-sessions/terminalfs/<session>`:

```sh
echo 'dotnet build' > <tree>/ctl/build
cat <tree>/cmd/build/wait       # blocks until it finishes
cat <tree>/cmd/build/exitcode
cat <tree>/cmd/build/stdout
```

Commands run as `agent` in the workspace. A deny list in
`~/.config/terminalfs/settings.json`, generated at build time from terminalfs's
own defaults, applies server-side too, so it holds whichever agent wrote the
command. Whatever was refused is explained in the tree.

### Claude Code

The `claude` stack installs the terminalfs plugin (hooks and skill) at container
start, and the stacks' policy fragments are merged into one managed policy,
`/etc/claude-code/managed-settings.json`. Managed settings outrank user, project
and command-line settings, so nothing inside a session can turn them off.
For the default namespace it reads:

```json
{
  "permissions": {
    "allow": [
      "Bash",
      "Read(//home/agent/mnt/terminalfs-sessions/terminalfs/**)",
      "Edit(//home/agent/mnt/terminalfs-sessions/terminalfs/**)",
      "Read(//home/agent/mnt/dotnetdocfs/**)",
      "Edit(//home/agent/mnt/dotnetdocfs/**)"
    ],
    "deny": [
      "Bash(sudo:*)", "Bash(su:*)", "Bash(doas:*)", "Bash(rm -rf /*)",
      "Bash(rm -rf ~*)", "Bash(rm -rf $HOME*)", "Bash(shutdown:*)",
      "Bash(reboot:*)", "Bash(halt:*)", "Bash(mkfs*)", "Bash(dd:*)",
      "Bash(diskutil:*)", "Bash(git push --force:*)", "Bash(git push -f:*)",
      "Bash(* | sh)", "Bash(* | bash)", "Bash(chmod -R 777 /*)",
      "PowerShell", "Monitor", "WebFetch", "WebFetch(domain:*)", "WebSearch"
    ],
    "additionalDirectories": [
      "/home/agent/mnt/terminalfs-sessions/terminalfs",
      "/home/agent/mnt/dotnetdocfs"
    ],
    "defaultMode": "acceptEdits"
  },
  "env": {
    "TERMINALFS_RUNTIME_DIR": "/home/agent/mnt/terminalfs-sessions",
    "TERMINALFS_CLAUDE_STRICT": "1",
    "DISABLE_AUTOUPDATER": "1"
  },
  "disableSkillShellExecution": true
}
```

(The deny list is wrapped here; the file has one entry per line.)

`Bash` is allowed, yet only tree-shaped commands run. The allow is what lets the
agent's one-call shape, a `Bash` call that writes to the tree and reads the
result, run without a prompt. The terminalfs hook then checks the command inside
that call against the same rules, and a deny beats an allow. Strict mode
(`TERMINALFS_CLAUDE_STRICT=1`) closes the rest: a `Bash` call that bypasses the
tree, or mixes a tree read with another command, is denied with a reason the
agent can read. Claude Code's own deny list still applies to the outer call.
`PowerShell`, `Monitor`, `WebFetch` and `WebSearch` are denied outright.

### opencode

`opencode-filesystem` has no shell or web tools of its own. The terminalfs
plugin is installed into `~/.config/opencode/plugins`, where opencode discovers
it, and it supplies the terminalfs skill. The policy in
`~/.config/opencode/opencode.json` allows the two mount roots and asks about any
other directory, and denies the same command shapes as the Claude policy.
opencode permission patterns are last-match-wins, so `"bash": {"*": "allow"}` comes
first and the deny shapes follow, e.g. `"sudo *": "deny"`, `"git push --force*": "deny"`,
`"* | sh": "deny"`. The full file mirrors every Bash deny above.

### dotnetdocfs

The `dotnetdocfs` server mounts itself at `~/mnt/dotnetdocfs` and serves a
skill. The entrypoint copies that skill into each agent's skills directory,
substituting the real path for the `<mount>` placeholder the server writes.

You still get a normal shell with passwordless sudo in the VS Code terminal. The
policy shapes the agents, not you. The container is the boundary, and mounted
repos are read-write.

On stop the entrypoint asks the 9P servers to unmount themselves before it stops
code-server, because a server killed while its own mount is up can hang the
container's shutdown in the kernel.

## Credentials and caches

No host credentials are mounted. The credentials volume `aide-<ns>-creds` is
mounted at `/home/agent/.credentials` and holds:

- Claude Code's config: `CLAUDE_CONFIG_DIR` points there, the documented way to
  keep `.claude.json` (the sign-in session and per-project trust state)
  alongside `.credentials.json`. Plugin state lives there too.
- opencode's data directory: `~/.local/share/opencode` is a symlink into it.
- the `gh` config: `GH_CONFIG_DIR` points there.

The cache volume `aide-<ns>-cache` is mounted at `/var/cache/aide` and holds
subdirectories for the tools that were selected: `nuget`, `npm`, `gomod`,
`gobuild`, `pip` and `xdg`. Tools are pointed at them with environment
variables, and `XDG_CACHE_HOME` goes to `xdg`. This keeps downloads out of the
snapshot, and so out of every commit.

Both volumes survive `reset` and image rebuilds, and are not shared between
namespaces. `remove` deletes them unless you pass `--keep-volumes`.

The baked-in policy and config are not on the volumes, so a rebuild always wins.

## Mounts and snapshots

`mount` and `umount` on a running namespace, and `start` with a changed mount
set, run the same procedure:

1. Validate. Host paths must exist and be directories (on Docker Desktop, inside
   the file sharing list). Container paths must be absolute and not overlap
   `/home/agent/.credentials`, `/var/cache/aide`, `/home/agent/mnt/*` or system
   directories. Nothing is touched if validation fails. Mounting over a non-empty
   target warns: its content is hidden until you unmount, not deleted.
2. Warn if busy. aide lists the processes `agent` is running beyond the
   entrypoint's own, such as an open `claude` or `opencode`, and asks to
   continue unless you pass `--yes`.
3. Stop the container, then commit it to `aide-<ns>:snap`. Stopping first gives
   a consistent filesystem.
4. Recreate the container from the snapshot with the new mounts.
5. If that fails, recreate it with the previous mounts and leave the saved
   configuration alone. The work is safe in the snapshot either way. If even
   that fails, run `aide ns:<name> start`.

What is kept: everything written inside the container's filesystem (packages,
files outside the mounts). Not kept: running processes, so open agent sessions
end. The volumes are separate and untouched.

Each commit adds an image layer, and overlay2 cannot run an image with more than
about 125. When a snapshot passes 100 layers aide flattens it to one with
`docker export | docker import`, reapplying the image configuration from the
base image. Flattening is slow, and the flat image no longer shares layers with
the base image, so it takes more disk. Most namespaces never reach it. If
flattening fails, aide warns and keeps the unflattened snapshot.

Disk use is the base image, one snapshot and the two volumes. `reset` removes the
container and snapshot, so the next `start` runs the base image again; the
volumes stay.

`docker commit` copies the container's environment into the snapshot image's
config, including the code-server password. It stays in your local Docker, but
do not push a snapshot image anywhere.

## Platforms

- **Linux.** Works as is. The container's `agent` user takes the invoking user's
  UID and GID, passed as build arguments, so files in bind mounts have the right
  owner. If the daemon has AppArmor, `--security-opt apparmor=unconfined` is
  added automatically for the stacks that mount 9P. The host kernel needs the
  9p modules. SELinux relabelling (`:z`) is not handled. Rootless Docker
  probably cannot mount 9P in a container at all.
- **macOS, Docker Desktop.** Host paths must be inside Docker Desktop's file
  sharing list, by default `/Users`, `/Volumes`, `/private`, `/tmp` and
  `/var/folders`. aide reads the list from Docker Desktop's settings and checks
  before stopping anything. A path outside it fails with:
  `<path> is outside Docker Desktop's file sharing list (...); add it under Settings > Resources > File sharing`.
- **Colima.** Only `$HOME` is shared by default.
- **OrbStack.** Expected to work.

## Migrating from ai-dev-env

The old containers (`aidev-*`) and volumes (`aidev-creds-*`) are not touched.
Create a namespace for the project instead, mount its directory, and remove the
old container when you are happy.

To keep a login, copy the old credentials volume into the new namespace's:

```sh
docker run --rm -v aidev-creds-x:/from -v aide-web-creds:/to alpine cp -a /from/. /to/
```

Files copied this way keep the old owner; the entrypoint hands them to `agent` on
the next start.

## Development

```sh
make build         # ./aide
make test          # go test ./...
make lint          # gofmt, go vet, shellcheck on the stack scripts
make golden        # regenerate the golden files
make integration   # AIDE_INTEGRATION=1, needs Docker; builds and runs the base stack
```

The code is in `cmd/aide` and `internal/`. Stacks live in
[internal/stacks](internal/stacks), one directory each:

```
internal/stacks/<name>/
  stack.json        description, order, requires, conflicts, suggests, run, cache
  Dockerfile        a fragment, with no FROM
  entrypoint.d/     NN-<name>.sh hooks, run as root by the entrypoint, in order
  files/            copied into the build context
  contrib/          claude-managed.json and opencode.json policy fragments
```

To add a stack, create the directory, give `stack.json` an `order` that puts it
where you want it, and list it with `aide stacks`. Policy fragments are deep
merged in stack order; arrays concatenate without duplicates and two different
values for one key are an error. Then run `make golden` and read the diff.

The golden files in
[internal/stacks/testdata/golden](internal/stacks/testdata/golden) hold the
generated Dockerfiles and merged policies for a few stack sets. They are the
review artifact: a change to a stack shows up as a change there.

## Licence

MIT. See [LICENSE](LICENSE).
