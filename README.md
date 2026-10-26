# dcctx

A small CLI to find your [dev container](https://containers.dev/) projects, start them, open a shell, and jump back to the previous one with `dcctx -`.

It is a thin convenience layer over the official [`devcontainer` CLI](https://github.com/devcontainers/cli) (`up` / `exec`). Single static binary, Go standard library only.

```console
$ dcctx -l
* ● infra-terraform  /home/me/code/infra-terraform
  ○ homelab          /home/me/code/homelab
  ○ web              /home/me/code/clients/web

$ dcctx homelab          # start (if needed) and open a shell
$ dcctx -                # back to the previous project
$ dcctx web npm test     # run a command in another project, no cd needed
```

## Is this for you?

Be realistic about what it is. Unlike `kubectx`, there is no global "current context" that makes a command hit the wrong target: a dev container is always derived from a folder. So `dcctx` is **not** solving a safety problem. It gives you:

- project discovery across several folders, with fuzzy picking (`fzf`) and running state (`●` / `○`)
- one-command start + shell (`devcontainer up` + `devcontainer exec`), from any directory
- a `cd -`-style toggle between the last two projects
- running a command inside another project's container without changing directory
- tab completion (bash, zsh, fish)

If you have only a handful of projects, a shell alias around `devcontainer up/exec` is probably enough. It starts to pay off with a dozen or more projects. VS Code users already get a recent-containers list in the editor; this is for the terminal.

## Requirements

- `docker` or `podman` on your `PATH` (podman is detected automatically and passed to the `devcontainer` CLI via `--docker-path`; override with `DCCTX_DOCKER=/path/to/engine`). Shell aliases like `alias docker=podman` don't count: the `devcontainer` CLI spawns the binary itself, so it must be a real executable on the `PATH`.
- the [`devcontainer` CLI](https://github.com/devcontainers/cli): `npm i -g @devcontainers/cli`
- optional: [`fzf`](https://github.com/junegunn/fzf) for the interactive picker (otherwise a numbered menu is used)

## Install

Download a binary from the [releases page](https://github.com/OWNER/dcctx/releases), verify it against `SHA256SUMS`, and put it on your `PATH`:

```bash
install dcctx-linux-amd64 ~/.local/bin/dcctx     # macOS: dcctx-darwin-arm64 / -amd64
```

Or build from source (Go 1.24+):

```bash
git clone https://github.com/OWNER/dcctx && cd dcctx
go build -o dcctx . && install dcctx ~/.local/bin/
```

Then tell it where your projects live and enable completion:

```bash
dcctx --add-root ~/code
echo 'eval "$(dcctx --completion)"' >> ~/.bashrc    # zsh / fish: dcctx --completion zsh|fish
```

## Usage

| Command | What it does |
| --- | --- |
| `dcctx` | pick a project (fzf), start its container, open a shell |
| `dcctx <name> [command…]` | open a project directly; optionally run a command instead of a shell |
| `dcctx .` | project in the current folder (also `./path`, `/path`) |
| `dcctx -` | switch to the previous project |
| `dcctx -c` | print the current project name (handy for your prompt) |
| `dcctx -l` | list projects: `●` running, `○` stopped, `?` unknown, `*` current |
| `dcctx -r [name]` | rebuild the container (default: current project) |
| `dcctx --add-root <path>` / `--roots` | manage search folders |
| `dcctx --completion [bash\|zsh\|fish]` | print shell completion |

Names can be abbreviated when unambiguous. For duplicate names use `folder/name`. The shell inside the container is `bash`; override with `DCCTX_SHELL=zsh`.

### How it works

- **Discovery:** looks for `.devcontainer/devcontainer.json` or `.devcontainer.json` up to 4 levels below each search folder (skipping `node_modules` and `.git`). The default search folder is `~/code`.
- **Running state:** one `docker ps` call filtered on the `devcontainer.local_folder` label. If Docker is unreachable, state shows as `?`.
- **State:** `~/.config/dcctx/roots` (search folders) and `~/.config/dcctx/state` (current and previous project). `XDG_CONFIG_HOME` is respected.

## Central dev container profiles (experimental)

A project can use a dev container configuration stored outside its repository.
Existing local `.devcontainer` projects remain supported.

For the AEL Azure profile, assuming the repositories live at `~/Projects` and
`~/git/engineering-devcontainers`:

```sh
export AEL_WORKSPACE_ROOT="$HOME/Projects"
dcctx --map-profile "$HOME/Projects/azure-engineering" \
  "$HOME/git/engineering-devcontainers/.devcontainer/azure/devcontainer.json" \
  "/workspaces/projects/azure-engineering"
dcctx azure-engineering
```

Mappings are stored locally in `~/.config/dcctx/profiles` (or `$XDG_CONFIG_HOME/dcctx/profiles`).
The third argument specifies the **existing** directory inside the container where
the shell or requested command should run. The external configuration must itself
mount that directory; dcctx does not add mounts. The AEL Azure profile uses
`AEL_WORKSPACE_ROOT` for the host bind mount, so the environment variable must
be available when invoking dcctx.

The profile is passed to the official Dev Container CLI with `--config` while
the selected project remains the `--workspace-folder`. Use `dcctx -r azure-engineering`
to rebuild. This feature requires validation with a real Docker/VS Code installation.

## Limitations

- Ambiguous names are rejected with a list of candidates; use `folder/name` to pick one.
- Running state depends on the `devcontainer.local_folder` label. Setups that don't set it (some Podman configurations) will show `?` or `○`.
- Tested against fake `devcontainer`/`docker` binaries in CI (`test/integration.sh`) and by hand; real-world setups vary. Issues and PRs welcome.

## Development

```bash
gofmt -l . && go vet ./... && go test ./...
go build -o dcctx . && BIN=./dcctx bash test/integration.sh
./build.sh          # cross-compile into dist/ with checksums
```

Releases are built by GitHub Actions when you push a tag like `v0.1.0`.

The module path in `go.mod` and the links in this README use the placeholder `OWNER`. Replace it with your GitHub user or organisation before publishing:

```bash
grep -rl OWNER . --exclude-dir=.git | xargs sed -i 's/OWNER/your-github-name/g'
```

After that, `go install github.com/your-github-name/dcctx@latest` works as well.

## License

MIT
# dcctx
