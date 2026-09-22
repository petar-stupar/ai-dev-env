# ai-dev-env

An isolated development container for agentic work. Browser-based VS Code, two
coding agents, and the .NET/Node/Python toolchain, with the agents' shell access
routed through a 9P filesystem instead of a shell tool.

One container per project. Run as many at once as you like.

```sh
./build.sh
cd ~/src/my-project && ~/src/ai-dev-env/run.sh
```

`run.sh` prints the URL. The password is `agent` and it publishes on
`127.0.0.1` only.

## What's inside

| | |
| --- | --- |
| VS Code Server | code-server 4.138, on port 8080 in the container |
| opencode | the 9P-filesystem build from [petar-stupar/opencode](https://github.com/petar-stupar/opencode) |
| Claude Code | latest, plus the `Anthropic.claude-code` VS Code extension |
| [terminalfs](https://github.com/petar-stupar/terminalfs) | runs shell commands as files, mounted at `~/mnt/terminalfs` |
| [dotnetdocfs](https://github.com/petar-stupar/dotnetdocfs) | .NET and NuGet API docs as files, mounted at `~/mnt/dotnetdocfs` |
| Toolchain | .NET 10 SDK, Node.js LTS + npm, Python 3, git, ripgrep, build-essential |

Builds for the host architecture. `linux/arm64` (Apple Silicon) and
`linux/amd64` both work.

## The point of it

Both agents are pushed towards the same shape: no shell tool, no web access, and
commands run by writing to a filesystem.

opencode's filesystem build has no shell or web tools to begin with. Claude Code
is brought into line with a managed settings policy at
`/etc/claude-code/managed-settings.json`, which denies `Bash`, `PowerShell`,
`Monitor`, `WebFetch` and `WebSearch`. Managed settings outrank user, project and
command-line settings, so nothing inside a session can turn them back on.

What replaces them is terminalfs:

```sh
echo 'dotnet build' > ~/mnt/terminalfs/ctl/build
cat ~/mnt/terminalfs/cmd/build/wait       # blocks until it finishes
cat ~/mnt/terminalfs/cmd/build/exitcode
cat ~/mnt/terminalfs/cmd/build/stdout
```

Commands run as `agent` in the workspace. A deny list at
`~/.config/terminalfs/settings.json`, generated at build time from terminalfs's
own defaults, blocks 17 destructive command shapes. `~/mnt/terminalfs/refused`
explains anything that was rejected.

Both mounts serve their own agent skill describing how to use them, so neither
agent is told how this works by hand. opencode reads them off the mount via
`skills.paths` in its global config. Claude Code only discovers skills under its
own config directory, so the entrypoint copies each one there, substituting the
real path for the `<mount>` placeholder the servers write.

You still get a normal root-capable shell in the VS Code terminal. The policy
shapes the agents, not you.

## Usage

```
./run.sh [DIRECTORY] [options]

  DIRECTORY          the project to work on; the current directory by default
  --port PORT        host port; the first free one from 8080 by default
  --name NAME        container name; derived from the directory by default
  --password PASS    VS Code Server password; "agent" by default
  --bind ADDRESS     host address to publish on; 127.0.0.1 by default
  --image IMAGE      image to run; ai-dev-env:latest by default
  --recreate         replace an existing container for this directory
  --stop             stop the container for this directory
  --rm               remove it, keeping its credentials volume
  --purge            remove it and its credentials volume
```

Running it again for the same directory restarts the existing container rather
than making a second one. Containers are named `aidev-<dir>-<hash>`, where the
hash is of the full path, so two projects with the same folder name don't
collide.

```sh
./build.sh --no-cache      # pick up new releases of the agents and tools
./build.sh --help          # pin specific versions
```

Everything is fetched from the network at build time, so `--no-cache` is how you
update.

## Credentials

No host credentials are mounted. You sign in inside the container, once per
project:

```sh
docker exec -it -u agent <container> bash -l
claude          # then /login
opencode auth login
```

Each project gets a docker named volume, `aidev-creds-<dir>-<hash>`, mounted at
`/home/agent/.credentials`. `CLAUDE_CONFIG_DIR` points into it, which is the
documented way to get Claude Code to keep `.claude.json` — the sign-in session
and per-project trust state — alongside `.credentials.json`. opencode's data
directory is symlinked into it for `auth.json`.

Logins therefore survive `docker rm` and image rebuilds, and don't leak between
projects. `--purge` deletes the volume when you want the login gone.

The baked-in config is deliberately *not* on the volume, so a rebuild always
wins: the 9P skills are rewritten into Claude Code's config directory on every
start, and opencode's `opencode.json` and terminalfs's deny list live in the
image.

## Layout

```
Dockerfile                             the image
build.sh                               build it
run.sh                                 run one container per project
scripts/entrypoint.sh                  starts the 9P servers, mounts them, drops to `agent`
config/claude/managed-settings.json    the tool policy; unoverridable from a session
config/opencode/opencode.json          skill paths and mount access for opencode
```

## How it runs

The container starts as root because mounting 9P needs `CAP_SYS_ADMIN`, which
`run.sh` grants. The entrypoint starts both 9P servers as `agent`, mounts them,
installs the skills, then drops to `agent` for code-server. Nothing that executes
your code, or the agents' code, runs as root.

## Notes

- `--cap-add SYS_ADMIN` is required for the 9P mounts. If you drop it the
  container still starts and the entrypoint warns; the agents just lose the
  mounts.
- terminalfs gives commands no stdin and no terminal, so interactive programs —
  editors, pagers, REPLs — don't work through it. Use the VS Code terminal.
- Claude Code telemetry is left at its defaults. To turn it off, add
  `DISABLE_TELEMETRY` and `DISABLE_ERROR_REPORTING` to the `env` block in
  `config/claude/managed-settings.json` and rebuild.
- Denying `Bash` has a documented side effect: Claude Code restores its `Glob`
  and `Grep` tools, which it normally leaves out in favour of shelling out. File
  search keeps working.

## Licence

MIT. See [LICENSE](LICENSE).
