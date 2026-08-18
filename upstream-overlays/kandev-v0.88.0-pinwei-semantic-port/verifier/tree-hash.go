// Overlay tree-hash helper. Builds an in-memory git tree from a working
// directory and writes the resulting tree object to a file. Does not contact
// any database, runtime, or remote. Run by verify.sh.
//
// Strategy:
//  1. Run `git init -q` in the workdir.
//  2. Run `git add -A -f` so .gitignore does not filter (matches the way the
//     real repository tracks files; the source tree includes files that
//     .gitignore would otherwise hide, like .env templates).
//  3. Run `git write-tree` and write the tree object to the outfile.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: tree-hash <workdir> <outfile>")
		os.Exit(2)
	}
	workdir, outfile := os.Args[1], os.Args[2]

	run := func(name string, args ...string) ([]byte, error) {
		cmd := exec.Command(name, args...)
		cmd.Dir = workdir
		return cmd.CombinedOutput()
	}
	if out, err := run("git", "init", "-q"); err != nil {
		fmt.Fprintf(os.Stderr, "git init failed: %v: %s\n", err, strings.TrimSpace(string(out)))
		os.Exit(2)
	}
	if out, err := run("git", "add", "-A", "-f"); err != nil {
		fmt.Fprintf(os.Stderr, "git add failed: %v: %s\n", err, strings.TrimSpace(string(out)))
		os.Exit(2)
	}
	out, err := run("git", "write-tree")
	if err != nil {
		fmt.Fprintf(os.Stderr, "git write-tree failed: %v: %s\n", err)
		os.Exit(2)
	}
	tree := strings.TrimSpace(string(out))
	if err := os.WriteFile(filepath.Clean(outfile), []byte(tree+"\n"), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write outfile failed: %v\n", err)
		os.Exit(2)
	}
	fmt.Printf("%s\n", tree)
}
