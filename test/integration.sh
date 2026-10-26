#!/usr/bin/env bash
# Integration test: runs dcctx against fake `devcontainer`, `docker` and `fzf` binaries.
# Usage: BIN=./dcctx test/integration.sh   (build the binary first: go build -o dcctx .)
set -uo pipefail

BIN="$(cd "$(dirname "${BIN:-./dcctx}")" && pwd)/$(basename "${BIN:-./dcctx}")"
[ -x "$BIN" ] || { echo "binary not found: $BIN (run: go build -o dcctx .)"; exit 2; }

T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
FAKEBIN="$T/fakebin"; mkdir -p "$FAKEBIN"
export FAKE_LOG="$T/calls.log" FAKE_RUNNING="$T/running"
export XDG_CONFIG_HOME="$T/cfg" HOME="$T/home"
mkdir -p "$HOME" "$T/code/alpha/.devcontainer" "$T/code/grp/beta" "$T/code/one/app/.devcontainer" "$T/code/two/app/.devcontainer"
echo '{}' > "$T/code/alpha/.devcontainer/devcontainer.json"
echo '{}' > "$T/code/grp/beta/.devcontainer.json"
echo '{}' > "$T/code/one/app/.devcontainer/devcontainer.json"
echo '{}' > "$T/code/two/app/.devcontainer/devcontainer.json"
: > "$FAKE_LOG"; echo "$T/code/alpha" > "$FAKE_RUNNING"

cat > "$FAKEBIN/devcontainer" <<'EOF'
#!/usr/bin/env bash
echo "devcontainer $*" >> "$FAKE_LOG"
[ "$1" = up ] && echo '{"outcome":"success"}'
[ "$1" = exec ] && echo "EXEC-RAN: $*"
exit 0
EOF
cat > "$FAKEBIN/docker" <<'EOF'
#!/usr/bin/env bash
cat "$FAKE_RUNNING" 2>/dev/null
EOF
cat > "$FAKEBIN/fzf" <<'EOF'
#!/usr/bin/env bash
head -1
EOF
chmod +x "$FAKEBIN"/*

pass=0; fail=0
ok()   { echo "PASS  $1"; pass=$((pass+1)); }
bad()  { echo "FAIL  $1"; [ -n "${2:-}" ] && echo "      $2"; fail=$((fail+1)); }
check() { if grep -qF -- "$3" <<<"$2"; then ok "$1"; else bad "$1" "expected: $3 | got: $2"; fi; }
absent() { if grep -qF -- "$3" <<<"$2"; then bad "$1 (unexpected: $3)"; else ok "$1"; fi; }
export PATH="$FAKEBIN:$PATH"

out=$("$BIN" --version); check "version" "$out" "dcctx"
out=$("$BIN" --add-root "$T/code" 2>&1); check "add-root" "$out" "Added"
out=$("$BIN" --add-root "$T/code" 2>&1); check "add-root dedupe" "$out" "Already present"
out=$("$BIN" --roots); check "roots lists new folder" "$out" "$T/code"

out=$("$BIN" -l 2>&1)
check "list: alpha running" "$out" "● alpha"
check "list: beta stopped" "$out" "○ beta"
check "list: path shown" "$out" "$T/code/alpha"

out=$("$BIN" alpha 2>&1)
check "open: up called" "$(cat "$FAKE_LOG")" "devcontainer up --workspace-folder $T/code/alpha"
check "open: exec with bash" "$out" "EXEC-RAN: exec --workspace-folder $T/code/alpha bash"
out=$("$BIN" -c); check "current = alpha" "$out" "alpha"

out=$("$BIN" be terraform plan 2>&1)
check "prefix + command" "$out" "EXEC-RAN: exec --workspace-folder $T/code/grp/beta terraform plan"
out=$("$BIN" -c); check "current = beta" "$out" "beta"

out=$("$BIN" - 2>&1); check "prev: back to alpha" "$out" "$T/code/alpha bash"
out=$("$BIN" - 2>&1); check "prev: beta again (swap)" "$out" "$T/code/grp/beta bash"

out=$("$BIN" app 2>&1); rc=$?
check "ambiguous: message" "$out" "ambiguous"
[ $rc -ne 0 ] && ok "ambiguous: non-zero exit" || bad "ambiguous: exit code"
out=$("$BIN" two/app 2>&1); check "folder/name" "$out" "$T/code/two/app bash"
out=$("$BIN" nope 2>&1); check "unknown project" "$out" "not found"

out=$(cd "$T/code/alpha" && "$BIN" . 2>&1); check "dcctx . in project folder" "$out" "$T/code/alpha bash"
out=$(cd "$T/code" && "$BIN" . 2>&1); check "dcctx . without config" "$out" "no dev container configuration"

: > "$FAKE_LOG"; "$BIN" -r alpha >/dev/null 2>&1
check "rebuild: flag" "$(cat "$FAKE_LOG")" "up --workspace-folder $T/code/alpha --remove-existing-container"
absent "rebuild: no exec" "$(cat "$FAKE_LOG")" "devcontainer exec"

out=$(DCCTX_SHELL=zsh "$BIN" alpha 2>&1); check "DCCTX_SHELL" "$out" "alpha zsh"
out=$("$BIN" 2>&1 </dev/null); check "fzf picker takes first line" "$out" "EXEC-RAN: exec --workspace-folder $T/code/alpha bash"

# Podman-only host: no docker on PATH -> --docker-path podman is passed through
MINBIN="$T/minbin"; mkdir -p "$MINBIN"
for tool in bash env cat head; do ln -sf "$(command -v $tool)" "$MINBIN/$tool"; done
cp "$FAKEBIN/devcontainer" "$FAKEBIN/fzf" "$MINBIN/"
cat > "$MINBIN/podman" <<'EOF'
#!/usr/bin/env bash
cat "$FAKE_RUNNING" 2>/dev/null
EOF
chmod +x "$MINBIN"/*
: > "$FAKE_LOG"
out=$(PATH="$MINBIN" "$BIN" alpha 2>&1)
check "podman-only: up gets --docker-path" "$(cat "$FAKE_LOG")" "devcontainer up --docker-path podman --workspace-folder $T/code/alpha"
check "podman-only: exec gets --docker-path" "$out" "EXEC-RAN: exec --docker-path podman --workspace-folder $T/code/alpha bash"
out=$(PATH="$MINBIN" "$BIN" -l 2>&1); check "podman-only: running state via podman" "$out" "● alpha"

# Neither docker nor podman -> clear error instead of a Node stack trace
rm "$MINBIN/podman"
out=$(PATH="$MINBIN" "$BIN" alpha 2>&1); rc=$?
check "no engine: clear error" "$out" "neither docker nor podman found"
[ $rc -ne 0 ] && ok "no engine: non-zero exit" || bad "no engine: exit code"

mv "$FAKEBIN/fzf" "$FAKEBIN/fzf.off"
out=$(echo 4 | "$BIN" 2>&1)
check "menu without fzf" "$out" "fzf not found"
check "menu picks entry 4 (beta, sorted by name)" "$out" "EXEC-RAN: exec --workspace-folder $T/code/grp/beta bash"
out=$(echo x | "$BIN" 2>&1); check "menu: invalid input" "$out" "invalid selection"

cat > "$FAKEBIN/docker" <<'EOF'
#!/usr/bin/env bash
exit 1
EOF
out=$("$BIN" -l 2>&1)
check "docker down: ? marker" "$out" "? alpha"
check "docker down: note" "$out" "Docker not reachable"

rm "$FAKEBIN/devcontainer"
out=$(PATH="$FAKEBIN:/usr/bin:/bin" "$BIN" alpha 2>&1); check "missing devcontainer CLI" "$out" "npm i -g @devcontainers/cli"

"$BIN" --completion bash > "$T/c.bash"
bash -n "$T/c.bash" && ok "bash completion syntax" || bad "bash completion syntax"
out=$("$BIN" --names); check "names" "$out" "alpha"; check "names: duplicates qualified" "$out" "one/app"
"$BIN" --completion fish >/dev/null && ok "fish completion" || bad "fish completion"
if command -v zsh >/dev/null; then
  "$BIN" --completion zsh > "$T/c.zsh"
  zsh -n "$T/c.zsh" && ok "zsh completion syntax" || bad "zsh completion syntax"
else
  echo "SKIP  zsh not installed"
fi

# External profile integration: no local devcontainer configuration
mkdir -p "$T/external-project" "$T/profiles"
echo '{}' > "$T/profiles/devcontainer.json"
out=$("$BIN" --map-profile "$T/external-project" "$T/profiles/devcontainer.json" "/workspaces/projects/external-project" 2>&1)
check "external profile: mapping registered" "$out" "Mapped"
out=$("$BIN" -l 2>&1)
check "external profile: listed" "$out" "external-project"
cp "$MINBIN/devcontainer" "$FAKEBIN/devcontainer"
: > "$FAKE_LOG"
out=$("$BIN" external-project 2>&1)
check "external profile: up uses config" "$(cat "$FAKE_LOG")" "--config $T/profiles/devcontainer.json"
check "external profile: exec uses config" "$out" "--config $T/profiles/devcontainer.json"
check "external profile: working directory" "$out" "/workspaces/projects/external-project"

echo; echo "Result: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
