package recap

import (
	"reflect"
	"testing"
)

func TestClassify(t *testing.T) {
	kinds := func(ms []Milestone) []string {
		var out []string
		for _, m := range ms {
			out = append(out, m.Kind+":"+m.Text)
		}
		return out
	}
	cases := []struct {
		name, command, output string
		failed                bool
		want                  []string
	}{
		{"commit", `git add -A && git commit -m "Fix the theme"`, "[main 1a2b3c4] Fix the theme\n 1 file changed", false,
			[]string{"commit:Fix the theme"}},
		{"root commit", `git commit -m init`, "[main (root-commit) 1a2b3c4] init", false, []string{"commit:init"}},
		{"quiet commit takes -m", `git add . && git commit -q -m "Pair an iPhone from the palette

Body"`, "", false, []string{"commit:Pair an iPhone from the palette"}},
		{"heredoc commit", "git commit -q -m \"$(cat <<'EOF'\nStream in batches\n\nBody\nEOF\n)\"", "", false,
			[]string{"commit:Stream in batches"}},
		{"git -C commit", `git -C /x/app commit -qm 'Tidy'`, "", false, []string{"commit:Tidy"}},
		{"nothing to commit", `git commit -m x`, "nothing to commit, working tree clean", false, nil},
		{"failed commit", `git commit -m x`, "Exit code 1", true, nil},
		{"git log mentioning commit isn't one", `git log --oneline | grep commit`, "abc fix", false, nil},
		{"pr opened", `gh pr create --title "Recap" --body x`, "https://github.com/ExManubis/claudewitz/pull/37\n", false,
			[]string{"pr:https://github.com/ExManubis/claudewitz/pull/37"}},
		{"pr merged", `gh pr merge 9 --merge --delete-branch`, "✓ Merged pull request ExManubis/claudewitz-ios#9 (Stream in batches)", false,
			[]string{"merge:ExManubis/claudewitz-ios#9 (Stream in batches)"}},
		{"quiet merge", `gh pr merge --squash 36`, "", false, []string{"merge:#36"}},
		{"tag pushed", `git tag v0.3.5 && git push origin v0.3.5`, "To github.com:ExManubis/claudewitz.git\n * [new tag]         v0.3.5 -> v0.3.5", false,
			[]string{"release:v0.3.5"}},
		{"quiet tag push", `git tag -a v0.3.1 -m "v0.3.1" a836fb9 && git push -q origin v0.3.1 && git ls-remote --tags origin v0.3.1`, "02d2eb3", false,
			[]string{"release:v0.3.1"}},
		{"branch push isn't a release", `git push -u origin claude/recap`, "", false, nil},
		{"escaped subject", "git commit -qm \"Add \\`claudewitz daemon\\` and a \\\"flag\\\"\"", "", false,
			[]string{"commit:Add `claudewitz daemon` and a \"flag\""}},
		{"release created", `gh release create v1.0.0 --generate-notes`, "https://github.com/a/b/releases/tag/v1.0.0", false,
			[]string{"release:v1.0.0"}},
		{"tests pass", `cd app && go test ./...`, "ok  \tgithub.com/x/y\t0.3s", false, []string{"tests:go test"}},
		{"tests fail", `go test ./internal/tui`, "--- FAIL: TestX\nFAIL", true, []string{"testsFailed:go test"}},
		{"failure hidden by a pipe", `go test ./... 2>&1 | tail -3`, "--- FAIL: TestX (0.01s)\nFAIL\tgithub.com/x\t0.2s", false,
			[]string{"testsFailed:go test"}},
		{"xcodebuild", `xcodebuild -scheme App -destination x test 2>&1 | tail`, "** TEST SUCCEEDED **", false, []string{"tests:xcodebuild test"}},
		{"swift failures", `swift test`, "Executed 12 tests, with 2 failures (0 unexpected)", false, []string{"testsFailed:swift test"}},
		{"zero failures pass", `swift test`, "Executed 12 tests, with 0 failures (0 unexpected)", false, []string{"tests:swift test"}},
		{"pytest", `python -m pytest -q`, "3 passed in 0.1s", false, []string{"tests:python -m pytest"}},
		{"npm run test", `npm run test -- --watch=false`, "Tests: 4 passed", false, []string{"tests:npm run test"}},
		{"test then commit", `go test ./... && git commit -qm "Green"`, "ok x", false, []string{"tests:go test", "commit:Green"}},
		{"a script that mentions a release isn't one", "python3 - <<'EOF'\nprint('git push -q origin v1.2.3 && go test ./...')\nEOF\ngit push -q origin main", "", false, nil},
		{"after the heredoc still counts", "cat > x <<EOF\nhi\nEOF\ngo test ./...", "ok", false, []string{"tests:go test"}},
		{"grep for test isn't a run", `grep -rn "go testing" .`, "", false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := kinds(Classify(c.command, c.output, c.failed)); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}
