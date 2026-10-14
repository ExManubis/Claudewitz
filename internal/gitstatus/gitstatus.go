// Package gitstatus reads a working tree's branch and change state.
package gitstatus

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// File is one changed path. X and Y are the index and worktree status
// letters from `git status` ('.' for unchanged, '?' for untracked).
type File struct {
	Path    string
	Orig    string // rename source
	X, Y    byte
	Added   int
	Removed int
	Binary  bool
}

// Untracked reports whether git doesn't know the file yet.
func (f File) Untracked() bool { return f.X == '?' }

// Conflicted reports an unmerged path.
func (f File) Conflicted() bool {
	return f.X == 'U' || f.Y == 'U' || (f.X == 'A' && f.Y == 'A') || (f.X == 'D' && f.Y == 'D')
}

// Code is the single most relevant status letter for display.
func (f File) Code() byte {
	switch {
	case f.Conflicted():
		return 'U'
	case f.Untracked():
		return '?'
	case f.X != '.' && f.X != ' ':
		return f.X
	default:
		return f.Y
	}
}

// Status is a snapshot of a repository.
type Status struct {
	IsRepo     bool
	Root       string
	Branch     string // empty when detached
	Head       string // commit sha ("" before the first commit)
	Upstream   string
	Ahead      int
	Behind     int
	Files      []File
	LastCommit string // "<short sha> <subject>"
	LastWhen   time.Time
}

// Detached reports a checked-out commit rather than a branch.
func (s Status) Detached() bool { return s.IsRepo && s.Branch == "" }

// Counts summarises the changes.
func (s Status) Counts() (staged, modified, untracked, conflicts int) {
	for _, f := range s.Files {
		switch {
		case f.Conflicted():
			conflicts++
		case f.Untracked():
			untracked++
		default:
			if f.X != '.' {
				staged++
			}
			if f.Y != '.' {
				modified++
			}
		}
	}
	return
}

// Dirty reports any change in the working tree or index.
func (s Status) Dirty() bool { return len(s.Files) > 0 }

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.quotePath=false"}, args...)...)
	cmd.Dir = dir
	// Never take the index lock: Claude may be running git at the same time.
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	out, err := cmd.Output()
	return string(out), err
}

// Read collects the status of the repository containing dir. A directory
// outside any repository yields a Status with IsRepo false and no error.
func Read(ctx context.Context, dir string) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	root, err := git(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return Status{}, nil
	}
	st := Status{IsRepo: true, Root: strings.TrimSpace(root)}

	out, err := git(ctx, dir, "status", "--porcelain=v2", "--branch", "--untracked-files=all")
	if err != nil {
		return st, err
	}
	parseStatus(out, &st)

	// Line counts for tracked changes, staged and unstaged together.
	base := "HEAD"
	if st.Head == "" {
		base = "--cached" // no commits yet
	}
	if ns, err := git(ctx, dir, "diff", "--numstat", "--no-renames", base); err == nil {
		applyNumstat(ns, &st)
	}

	if st.Head != "" {
		if lc, err := git(ctx, dir, "log", "-1", "--format=%h %ct %s"); err == nil {
			parts := strings.SplitN(strings.TrimSpace(lc), " ", 3)
			if len(parts) == 3 {
				st.LastCommit = parts[0] + " " + parts[2]
				if sec, err := strconv.ParseInt(parts[1], 10, 64); err == nil {
					st.LastWhen = time.Unix(sec, 0)
				}
			}
		}
	}
	return st, nil
}

// parseStatus reads `git status --porcelain=v2 --branch` output.
func parseStatus(out string, st *Status) {
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		switch line[0] {
		case '#':
			f := strings.Fields(line)
			if len(f) < 3 {
				continue
			}
			switch f[1] {
			case "branch.oid":
				if f[2] != "(initial)" {
					st.Head = f[2]
				}
			case "branch.head":
				if f[2] != "(detached)" {
					st.Branch = f[2]
				}
			case "branch.upstream":
				st.Upstream = f[2]
			case "branch.ab":
				if len(f) >= 4 {
					st.Ahead, _ = strconv.Atoi(strings.TrimPrefix(f[2], "+"))
					st.Behind, _ = strconv.Atoi(strings.TrimPrefix(f[3], "-"))
				}
			}
		case '1':
			// 1 XY sub mH mI mW hH hI path
			if f := strings.SplitN(line, " ", 9); len(f) == 9 && len(f[1]) == 2 {
				st.Files = append(st.Files, File{Path: f[8], X: f[1][0], Y: f[1][1]})
			}
		case '2':
			// 2 XY sub mH mI mW hH hI Xscore path<TAB>origPath
			if f := strings.SplitN(line, " ", 10); len(f) == 10 && len(f[1]) == 2 {
				path, orig, _ := strings.Cut(f[9], "\t")
				st.Files = append(st.Files, File{Path: path, Orig: orig, X: f[1][0], Y: f[1][1]})
			}
		case 'u':
			// u XY sub m1 m2 m3 mW h1 h2 h3 path
			if f := strings.SplitN(line, " ", 11); len(f) == 11 && len(f[1]) == 2 {
				st.Files = append(st.Files, File{Path: f[10], X: f[1][0], Y: f[1][1]})
			}
		case '?':
			st.Files = append(st.Files, File{Path: strings.TrimPrefix(line, "? "), X: '?', Y: '?'})
		}
	}
}

// applyNumstat fills in added/removed counts from `git diff --numstat`.
func applyNumstat(out string, st *Status) {
	idx := map[string]int{}
	for i, f := range st.Files {
		idx[f.Path] = i
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) != 3 {
			continue
		}
		i, ok := idx[f[2]]
		if !ok {
			continue
		}
		if f[0] == "-" {
			st.Files[i].Binary = true
			continue
		}
		st.Files[i].Added, _ = strconv.Atoi(f[0])
		st.Files[i].Removed, _ = strconv.Atoi(f[1])
	}
}

// Diff returns the unified diff of one changed file against HEAD, staged
// and unstaged changes together (against the index before the first
// commit). Untracked files diff against nothing, so every line is added.
// Binary files yield git's one-line "Binary files … differ" with no hunks.
func Diff(ctx context.Context, root string, f File) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	args := []string{"diff", "--no-color", "--no-ext-diff", "-M"}
	switch {
	case f.Untracked():
		args = append(args, "--no-index", "--", os.DevNull, f.Path)
	default:
		if _, err := git(ctx, root, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
			args = append(args, "HEAD")
		} else {
			args = append(args, "--cached") // no commits yet
		}
		args = append(args, "--")
		if f.Orig != "" {
			args = append(args, f.Orig)
		}
		args = append(args, f.Path)
	}
	out, err := git(ctx, root, args...)
	// --no-index exits 1 when the files differ, which they always do here.
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 && out != "" {
		err = nil
	}
	return out, err
}
