// dcctx – dev container context switcher.
//
// Like kubectx, but for dev containers: find projects, start the container,
// open a shell, and jump back to the previous project with "dcctx -".
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

const maxDepth = 4 // maximum search depth below a search folder

var errCancelled = errors.New("cancelled")

type project struct {
	Name string
	Path string
	Profile string
	Workdir string
}

type config struct {
	rootsFile string // one search folder per line
	stateFile string // line 1: current project, line 2: previous project
	profilesFile string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		if !errors.Is(err, errCancelled) {
			fmt.Fprintln(os.Stderr, "dcctx:", err)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return c.cmdPick()
	}
	switch a := args[0]; a {
	case "-h", "--help", "help":
		usage()
		return nil
	case "--version":
		fmt.Println("dcctx", version)
		return nil
	case "-l", "--list":
		return c.cmdList()
	case "--names":
		for _, n := range completionNames(c.projects()) {
			fmt.Println(n)
		}
		return nil
	case "--roots":
		for _, r := range c.readRoots() {
			fmt.Println(r)
		}
		return nil
	case "--map-profile":
		if len(args)!=4{return errors.New("usage: dcctx --map-profile <project> <devcontainer.json> <container-workdir>")}
		return c.mapProfile(args[1],args[2],args[3])
	case "--add-root":
		if len(args) < 2 {
			return errors.New("missing path: dcctx --add-root <path>")
		}
		return c.addRoot(args[1])
	case "-c", "--current":
		if cur, _ := c.readState(); cur != "" {
			fmt.Println(filepath.Base(cur))
		}
		return nil
	case "-":
		return c.cmdPrev()
	case "-r", "--rebuild":
		return c.cmdRebuild(args[1:])
	case "--completion":
		shell := "bash"
		if len(args) > 1 {
			shell = args[1]
		}
		return printCompletion(shell)
	default:
		if strings.HasPrefix(a, "-") {
			return fmt.Errorf("unknown option %q (try dcctx -h)", a)
		}
		return c.cmdOpen(a, args[1:])
	}
}

func usage() {
	fmt.Print(`dcctx – dev container context switcher

  dcctx                     pick a project (fzf), start its container, open a shell
  dcctx <name> [command...] open a project directly, optionally run a command instead of a shell
  dcctx .                   project in the current folder (also: ./path, /path)
  dcctx -                   switch to the previous project
  dcctx -c                  print the current project name (for your prompt)
  dcctx -l                  list projects (● running, ○ stopped, ? unknown, * current)
  dcctx -r [name]           rebuild the container (default: current project)
  dcctx --add-root <path>   add a search folder
  dcctx --map-profile <project> <config> <container-workdir>
                             use a central profile for a project
  dcctx --roots             show search folders
  dcctx --completion [sh]   print shell completion: bash (default), zsh, fish
  dcctx --version

Names can be abbreviated if unambiguous; for duplicate names use "folder/name".
Shell inside the container: $DCCTX_SHELL (default: bash).
Container engine: $DCCTX_DOCKER (default: docker, else podman if found in PATH).
`)
}

// ---------------------------------------------------------------- configuration

func loadConfig() (*config, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		base = filepath.Join(home, ".config")
	}
	dir := filepath.Join(base, "dcctx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &config{
		rootsFile: filepath.Join(dir, "roots"),
		stateFile: filepath.Join(dir, "state"),
		profilesFile: filepath.Join(dir, "profiles"),
	}, nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// readRoots reads the search folders; without a roots file ~/code is the default.
func (c *config) readRoots() []string {
	b, err := os.ReadFile(c.rootsFile)
	if err != nil {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return nil
		}
		return []string{filepath.Join(home, "code")}
	}
	var roots []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		roots = append(roots, expandHome(l))
	}
	return roots
}

func (c *config) addRoot(path string) error {
	abs, err := filepath.Abs(expandHome(path))
	if err != nil {
		return err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", abs)
	}
	roots := c.readRoots()
	for _, r := range roots {
		if r == abs {
			fmt.Println("Already present:", abs)
			return nil
		}
	}
	roots = append(roots, abs)
	if err := os.WriteFile(c.rootsFile, []byte(strings.Join(roots, "\n")+"\n"), 0o644); err != nil {
		return err
	}
	fmt.Println("Added:", abs)
	return nil
}

func (c *config) readState() (cur, prev string) {
	b, err := os.ReadFile(c.stateFile)
	if err != nil {
		return "", ""
	}
	lines := strings.Split(string(b), "\n")
	if len(lines) > 0 {
		cur = strings.TrimSpace(lines[0])
	}
	if len(lines) > 1 {
		prev = strings.TrimSpace(lines[1])
	}
	return cur, prev
}

// writeState sets the current project and remembers the old one as previous.
func (c *config) writeState(newCur string) error {
	cur, prev := c.readState()
	if newCur != cur {
		prev = cur
	}
	tmp := c.stateFile + ".tmp"
	if err := os.WriteFile(tmp, []byte(newCur+"\n"+prev+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, c.stateFile)
}


func (c *config) mapped() []project {
 b,err:=os.ReadFile(c.profilesFile);if err!=nil{return nil}
 var out []project
 for _,line:=range strings.Split(string(b),"\n"){
  fields:=strings.Split(line,"\t");if len(fields)!=3{continue}
  out=append(out,project{Name:filepath.Base(fields[0]),Path:fields[0],Profile:fields[1],Workdir:fields[2]})
 }
 return out
}
func (c *config) mapProfile(path,profile,workdir string) error {
 p,err:=filepath.Abs(expandHome(path));if err!=nil{return err}
 f,err:=filepath.Abs(expandHome(profile));if err!=nil{return err}
 if st,e:=os.Stat(p);e!=nil||!st.IsDir(){return fmt.Errorf("missing project directory: %s",p)}
 if st,e:=os.Stat(f);e!=nil||st.IsDir()||filepath.Base(f)!="devcontainer.json"{return fmt.Errorf("missing devcontainer.json: %s",f)}
 if !filepath.IsAbs(workdir){return errors.New("container workdir must be absolute")}
 for _,v:=range []string{p,f,workdir}{if strings.ContainsAny(v,"\t\r\n"){return errors.New("tabs/newlines are not allowed in paths")}}
 ps:=c.mapped();found:=false
 for i:=range ps{if ps[i].Path==p{ps[i].Profile=f;ps[i].Workdir=workdir;found=true}}
 if !found{ps=append(ps,project{Name:filepath.Base(p),Path:p,Profile:f,Workdir:workdir})}
 var lines []string
 for _,v:=range ps{lines=append(lines,strings.Join([]string{v.Path,v.Profile,v.Workdir},"\t"))}
 if err:=os.WriteFile(c.profilesFile,[]byte(strings.Join(lines,"\n")+"\n"),0600);err!=nil{return err}
 fmt.Printf("Mapped %s to %s\n",p,f);return nil
}
func (c *config) projects() []project {
 ps:=discover(c.readRoots(),maxDepth);index:=map[string]int{}
 for i,p:=range ps{index[p.Path]=i}
 for _,p:=range c.mapped(){if st,e:=os.Stat(p.Path);e!=nil||!st.IsDir(){continue};if i,ok:=index[p.Path];ok{ps[i]=p}else{index[p.Path]=len(ps);ps=append(ps,p)}}
 sort.Slice(ps,func(i,j int)bool{if ps[i].Name!=ps[j].Name{return ps[i].Name<ps[j].Name};return ps[i].Path<ps[j].Path})
 return ps
}
func (c *config) resolveProject(q string)(project,error){
 ps:=c.projects()
 if looksLikePath(q){p,e:=filepath.Abs(expandHome(q));if e!=nil{return project{},e};for _,v:=range ps{if v.Path==p{return v,nil}}}
 return resolve(q,ps)
}
func profileArgs(p project)[]string{if p.Profile==""{return nil};return []string{"--config",p.Profile}}

// ---------------------------------------------------------------- discovery

func hasConfig(dir string) bool {
	for _, f := range []string{
		filepath.Join(dir, ".devcontainer", "devcontainer.json"),
		filepath.Join(dir, ".devcontainer.json"),
	} {
		if fi, err := os.Stat(f); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}

func pathDepth(rel string) int {
	return strings.Count(rel, string(filepath.Separator)) + 1
}

// discover finds projects with a dev container configuration below the search folders.
func discover(roots []string, depth int) []project {
	seen := map[string]bool{}
	var out []project
	for _, root := range roots {
		root = filepath.Clean(root)
		if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
			continue
		}
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if d.IsDir() {
				if p == root {
					return nil
				}
				if n := d.Name(); n == "node_modules" || n == ".git" {
					return fs.SkipDir
				}
				if rel, e := filepath.Rel(root, p); e == nil && pathDepth(rel) >= depth {
					return fs.SkipDir
				}
				return nil
			}
			var dir string
			switch {
			case d.Name() == "devcontainer.json" && filepath.Base(filepath.Dir(p)) == ".devcontainer":
				dir = filepath.Dir(filepath.Dir(p))
			case d.Name() == ".devcontainer.json":
				dir = filepath.Dir(p)
			default:
				return nil
			}
			if !seen[dir] {
				seen[dir] = true
				out = append(out, project{Name: filepath.Base(dir), Path: dir})
			}
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// runningFolders returns the project folders that have a running container
// (a single Docker query). The bool is false when Docker could not be queried.
func runningFolders() (map[string]bool, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, engine(), "ps",
		"--filter", "label=devcontainer.local_folder",
		"--format", `{{.Label "devcontainer.local_folder"}}`).Output()
	m := map[string]bool{}
	if err != nil {
		return m, false
	}
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			m[l] = true
		}
	}
	return m, true
}

func mark(running, known bool) string {
	switch {
	case !known:
		return "?"
	case running:
		return "●"
	default:
		return "○"
	}
}

// ---------------------------------------------------------------- resolving

func looksLikePath(q string) bool {
	return q == "." || q == ".." || filepath.IsAbs(q) ||
		strings.HasPrefix(q, "./") || strings.HasPrefix(q, "../") || strings.HasPrefix(q, "~")
}

// resolve finds a project by path, exact name, "folder/name" or unambiguous prefix.
func resolve(query string, ps []project) (project, error) {
	if looksLikePath(query) {
		abs, err := filepath.Abs(expandHome(query))
		if err == nil && hasConfig(abs) {
			return project{Name: filepath.Base(abs), Path: abs}, nil
		}
		return project{}, fmt.Errorf("%s contains no dev container configuration", query)
	}

	var hits []project
	for _, p := range ps {
		if p.Name == query {
			hits = append(hits, p)
		}
	}
	if len(hits) == 0 && strings.Contains(query, "/") {
		q := "/" + filepath.ToSlash(query)
		for _, p := range ps {
			if strings.HasSuffix(filepath.ToSlash(p.Path), q) {
				hits = append(hits, p)
			}
		}
	}
	if len(hits) == 0 {
		for _, p := range ps {
			if strings.HasPrefix(p.Name, query) {
				hits = append(hits, p)
			}
		}
	}
	switch len(hits) {
	case 0:
		return project{}, fmt.Errorf("project %q not found (run dcctx -l)", query)
	case 1:
		return hits[0], nil
	default:
		var sb strings.Builder
		fmt.Fprintf(&sb, "%q is ambiguous:", query)
		for _, p := range hits {
			fmt.Fprintf(&sb, "\n  %s  %s", p.Name, p.Path)
		}
		return project{}, errors.New(sb.String())
	}
}

// completionNames returns names for shell completion; duplicate names are qualified as "folder/name".
func completionNames(ps []project) []string {
	count := map[string]int{}
	for _, p := range ps {
		count[p.Name]++
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range ps {
		n := p.Name
		if count[n] > 1 {
			n = filepath.Base(filepath.Dir(p.Path)) + "/" + n
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------- picker

func pick(ps []project, running map[string]bool, known bool) (project, error) {
	if len(ps) == 0 {
		return project{}, errors.New("no projects found – add a search folder with: dcctx --add-root <path>")
	}
	lines := make([]string, len(ps))
	for i, p := range ps {
		lines[i] = fmt.Sprintf("%s %s\t%s", mark(running[p.Path], known), p.Name, p.Path)
	}

	if fzf, err := exec.LookPath("fzf"); err == nil {
		cmd := exec.Command(fzf, "--delimiter", "\t", "--prompt", "dev container> ")
		cmd.Stdin = strings.NewReader(strings.Join(lines, "\n") + "\n")
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		if err != nil {
			return project{}, errCancelled
		}
		parts := strings.SplitN(strings.TrimRight(string(out), "\r\n"), "\t", 2)
		if len(parts) == 2 {
			for _, p := range ps {
				if p.Path == parts[1] {
					return p, nil
				}
			}
		}
		return project{}, errCancelled
	}

	fmt.Fprintln(os.Stderr, "(fzf not found – falling back to a simple menu)")
	for i, l := range lines {
		fmt.Fprintf(os.Stderr, "%2d) %s\n", i+1, strings.ReplaceAll(l, "\t", "  "))
	}
	fmt.Fprint(os.Stderr, "Project: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return project{}, errCancelled
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(ps) {
		return project{}, errors.New("invalid selection")
	}
	return ps[n-1], nil
}

// ---------------------------------------------------------------- commands

func (c *config) cmdPick() error {
	ps := c.projects()
	running, known := runningFolders()
	p, err := pick(ps, running, known)
	if err != nil {
		return err
	}
	return c.enter(p, nil)
}

func (c *config) cmdOpen(query string, args []string) error {
	p, err := c.resolveProject(query)
	if err != nil {
		return err
	}
	return c.enter(p, args)
}

func (c *config) cmdPrev() error {
	_, prev := c.readState()
	if prev == "" {
		return errors.New("no previous project")
	}
	if fi, err := os.Stat(prev); err != nil || !fi.IsDir() {
		return fmt.Errorf("previous project no longer exists: %s", prev)
	}
	p,err:=c.resolveProject(prev);if err!=nil{return err};return c.enter(p,nil)
}

func (c *config) cmdRebuild(args []string) error {
	var p project
	if len(args) > 0 {
		var err error
		if p, err = c.resolveProject(args[0]); err != nil {
			return err
		}
	} else {
		cur, _ := c.readState()
		if cur == "" {
			return errors.New("no current project – pass a name: dcctx -r <name>")
		}
		var err error
		p,err=c.resolveProject(cur);if err!=nil{return err}
	}
	dc, err := devcontainerBin()
	if err != nil {
		return err
	}
	if err := requireEngine(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "↻ Rebuild: %s\n", p.Name)
	rbArgs := append([]string{"up"}, engineArgs()...)
	cmd := exec.Command(dc, append(append(rbArgs, "--workspace-folder", p.Path), append(profileArgs(p), "--remove-existing-container")...)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("devcontainer up failed: %w", err)
	}
	return c.writeState(p.Path)
}

func (c *config) cmdList() error {
	ps := c.projects()
	if len(ps) == 0 {
		fmt.Fprintln(os.Stderr, "No projects found. Search folders: dcctx --roots, add one: dcctx --add-root <path>")
		return nil
	}
	running, known := runningFolders()
	cur, _ := c.readState()
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	for _, p := range ps {
		star := " "
		if p.Path == cur {
			star = "*"
		}
		fmt.Fprintf(tw, "%s %s %s\t%s\n", star, mark(running[p.Path], known), p.Name, p.Path)
	}
	tw.Flush()
	if !known {
		fmt.Fprintln(os.Stderr, "Note: Docker not reachable – running state unknown (?).")
	}
	return nil
}

func devcontainerBin() (string, error) {
	dc, err := exec.LookPath("devcontainer")
	if err != nil {
		return "", errors.New("devcontainer CLI not found – install it with: npm i -g @devcontainers/cli")
	}
	return dc, nil
}

// engine returns the container CLI to use: $DCCTX_DOCKER if set, otherwise
// "docker" if it is on the PATH, otherwise "podman" if that is. If neither is
// found it returns "docker" (callers check with engineAvailable).
func engine() string {
	if e := os.Getenv("DCCTX_DOCKER"); e != "" {
		return e
	}
	for _, n := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(n); err == nil {
			return n
		}
	}
	return "docker"
}

func engineAvailable() bool {
	_, err := exec.LookPath(engine())
	return err == nil
}

// engineArgs returns the devcontainer CLI flags needed to use a non-docker engine.
func engineArgs() []string {
	if e := engine(); filepath.Base(e) != "docker" {
		return []string{"--docker-path", e}
	}
	return nil
}

func requireEngine() error {
	if !engineAvailable() {
		return fmt.Errorf("neither docker nor podman found in PATH – install one or set DCCTX_DOCKER to its path (current: %q)", engine())
	}
	return nil
}

// enter starts the container (if needed) and opens a shell or runs a command in it.
func (c *config) enter(p project, args []string) error {
	dc, err := devcontainerBin()
	if err != nil {
		return err
	}
	if err := requireEngine(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "→ %s\n", p.Name)
	upArgs := append([]string{"up"}, engineArgs()...)
	up := exec.Command(dc, append(append(upArgs, "--workspace-folder", p.Path), profileArgs(p)...)...)
	up.Stderr = os.Stderr // stdout (the JSON result) is discarded
	if err := up.Run(); err != nil {
		return fmt.Errorf("devcontainer up failed: %w", err)
	}
	if err := c.writeState(p.Path); err != nil {
		return err
	}
	if len(args) == 0 {
		sh := os.Getenv("DCCTX_SHELL")
		if sh == "" {
			sh = "bash"
		}
		args = []string{sh}
	}
	argv := []string{"devcontainer", "exec"}
	argv = append(argv, engineArgs()...)
	argv = append(argv, "--workspace-folder", p.Path)
	argv = append(argv, profileArgs(p)...)
	if p.Workdir!=""{argv=append(argv,"sh","-c","cd \"$1\" && shift && exec \"$@\"", "dcctx",p.Workdir)}
	argv = append(argv, args...)
	return execReplace(dc, argv)
}

// ---------------------------------------------------------------- completion

func printCompletion(shell string) error {
	switch shell {
	case "bash":
		fmt.Print(`_dcctx() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=( $(compgen -W "$(dcctx --names) - -l -c -r --add-root --roots --completion -h" -- "$cur") )
}
complete -F _dcctx dcctx
`)
	case "zsh":
		fmt.Print(`_dcctx() {
  local -a names opts
  names=(${(f)"$(dcctx --names)"})
  opts=('-:previous project' '-l:list projects' '-c:current project' '-r:rebuild container' '--add-root:add search folder' '--roots:show search folders' '--completion:print completion')
  _describe -t projects 'project' names
  _describe -t options 'option' opts
}
compdef _dcctx dcctx
`)
	case "fish":
		fmt.Print(`complete -c dcctx -f -a '(dcctx --names)'
complete -c dcctx -f -a '- -l -c -r --add-root --roots --completion'
`)
	default:
		return fmt.Errorf("unknown shell %q (bash, zsh, fish)", shell)
	}
	return nil
}
