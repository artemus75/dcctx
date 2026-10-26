package main

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestEngine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("PATH lookup of shell stubs is not portable to Windows")
	}
	bin := t.TempDir()
	stub := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("DCCTX_DOCKER", "")

	if engineAvailable() {
		t.Fatal("no engine on PATH: engineAvailable should be false")
	}
	if err := requireEngine(); err == nil || !strings.Contains(err.Error(), "neither docker nor podman") {
		t.Fatalf("requireEngine err = %v", err)
	}

	stub("podman")
	if engine() != "podman" || !reflect.DeepEqual(engineArgs(), []string{"--docker-path", "podman"}) {
		t.Fatalf("podman only: engine=%q args=%v", engine(), engineArgs())
	}

	stub("docker")
	if engine() != "docker" || engineArgs() != nil {
		t.Fatalf("docker preferred: engine=%q args=%v", engine(), engineArgs())
	}

	t.Setenv("DCCTX_DOCKER", filepath.Join(bin, "podman"))
	if want := []string{"--docker-path", filepath.Join(bin, "podman")}; !reflect.DeepEqual(engineArgs(), want) {
		t.Fatalf("override: args=%v want %v", engineArgs(), want)
	}
}

func mk(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(ps []project) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Name)
	}
	return out
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "alpha/.devcontainer/devcontainer.json")
	mk(t, root, "grp/beta/.devcontainer.json")
	mk(t, root, "grp/gamma/.devcontainer/devcontainer.json")
	mk(t, root, "node_modules/x/.devcontainer/devcontainer.json") // skipped
	mk(t, root, ".git/y/.devcontainer/devcontainer.json")         // skipped
	mk(t, root, "a/b/c/d/e/.devcontainer/devcontainer.json")      // too deep
	mk(t, root, "alpha/.devcontainer/other/devcontainer.json")    // nested config ignored

	got := names(discover([]string{root, filepath.Join(root, "missing")}, 4))
	want := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("discover = %v, want %v", got, want)
	}
}

func TestDiscoverRootIsProject(t *testing.T) {
	root := t.TempDir()
	mk(t, root, ".devcontainer/devcontainer.json")
	ps := discover([]string{root}, 4)
	if len(ps) != 1 || ps[0].Path != root {
		t.Fatalf("got %v", ps)
	}
}

func TestResolve(t *testing.T) {
	ps := []project{
		{"web", "/x/web"}, {"web-api", "/x/web-api"}, {"db", "/x/db"},
		{"app", "/one/app"}, {"app", "/two/app"},
	}
	cases := []struct {
		q       string
		want    string
		wantErr string
	}{
		{"web", "/x/web", ""}, // exact beats prefix
		{"d", "/x/db", ""},    // unambiguous prefix
		{"w", "", "ambiguous"},
		{"app", "", "ambiguous"},
		{"two/app", "/two/app", ""}, // folder/name
		{"zzz", "", "not found"},
	}
	for _, tc := range cases {
		p, err := resolve(tc.q, ps)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("resolve(%q) err = %v, want %q", tc.q, err, tc.wantErr)
			}
			continue
		}
		if err != nil || p.Path != tc.want {
			t.Errorf("resolve(%q) = %v, %v; want %s", tc.q, p, err, tc.want)
		}
	}
}

func TestResolvePath(t *testing.T) {
	dir := t.TempDir()
	mk(t, dir, ".devcontainer/devcontainer.json")
	p, err := resolve(dir, nil)
	if err != nil || p.Path != dir {
		t.Fatalf("got %v, %v", p, err)
	}
	if _, err := resolve(t.TempDir(), nil); err == nil {
		t.Fatal("path without configuration should fail")
	}
}

func TestCompletionNames(t *testing.T) {
	ps := []project{{"app", "/one/app"}, {"app", "/two/app"}, {"db", "/x/db"}}
	got := completionNames(ps)
	want := []string{"db", "one/app", "two/app"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestState(t *testing.T) {
	c := &config{stateFile: filepath.Join(t.TempDir(), "state")}
	step := func(set, wantCur, wantPrev string) {
		t.Helper()
		if err := c.writeState(set); err != nil {
			t.Fatal(err)
		}
		cur, prev := c.readState()
		if cur != wantCur || prev != wantPrev {
			t.Fatalf("nach %q: (%q,%q), want (%q,%q)", set, cur, prev, wantCur, wantPrev)
		}
	}
	step("a", "a", "")
	step("b", "b", "a")
	step("c", "c", "b")
	step("c", "c", "b") // same project keeps prev unchanged
	step("b", "b", "c") // "dcctx -" swaps
}

func TestRoots(t *testing.T) {
	dir := t.TempDir()
	c := &config{rootsFile: filepath.Join(dir, "roots")}
	home, _ := os.UserHomeDir()

	// Without a file: default ~/code
	if got := c.readRoots(); len(got) != 1 || got[0] != filepath.Join(home, "code") {
		t.Fatalf("default roots = %v", got)
	}
	os.WriteFile(c.rootsFile, []byte("# comment\n\n~/work\n/abs\n"), 0o644)
	got := c.readRoots()
	want := []string{filepath.Join(home, "work"), "/abs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	// addRoot: dedupes, keeps existing entries
	extra := t.TempDir()
	if err := c.addRoot(extra); err != nil {
		t.Fatal(err)
	}
	if err := c.addRoot(extra); err != nil {
		t.Fatal(err)
	}
	got = c.readRoots()
	if len(got) != 3 || got[2] != extra {
		t.Fatalf("after add: %v", got)
	}
	if err := c.addRoot(filepath.Join(extra, "nope")); err == nil {
		t.Fatal("missing folder should fail")
	}
}

func TestCompletionShells(t *testing.T) {
	for _, sh := range []string{"bash", "zsh", "fish"} {
		if err := printCompletion(sh); err != nil {
			t.Errorf("%s: %v", sh, err)
		}
	}
	if err := printCompletion("csh"); err == nil {
		t.Error("csh should be rejected")
	}
}
