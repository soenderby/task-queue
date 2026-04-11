package tq

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

var tqBinary string

func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "tq-bin-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmp)

	tqBinary = filepath.Join(tmp, "tq")
	build := exec.Command("go", "build", "-o", tqBinary, "./cmd/tq/")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		panic("failed to build tq binary: " + err.Error() + "\n" + string(out))
	}

	os.Exit(m.Run())
}

func runCLI(t *testing.T, dir string, env map[string]string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	cmd := exec.Command(tqBinary, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var outBuf bytes.Buffer
	var errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	if err == nil {
		return outBuf.String(), errBuf.String(), 0
	}
	if e, ok := err.(*exec.ExitError); ok {
		return outBuf.String(), errBuf.String(), e.ExitCode()
	}
	t.Fatalf("failed to run command: %v", err)
	return "", "", -1
}

func mustInitCLIWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	_, stderr, code := runCLI(t, dir, nil, "init", "--prefix", "orca")
	if code != 0 {
		t.Fatalf("tq init failed (code=%d): %s", code, stderr)
	}
	return dir
}

func TestCLIInitCreateShowRoundTrip(t *testing.T) {
	dir := mustInitCLIWorkspace(t)

	stdout, stderr, code := runCLI(t, dir, nil, "create", "Fix login timeout")
	if code != 0 {
		t.Fatalf("create failed: %s", stderr)
	}
	id := strings.TrimSpace(stdout)
	if !strings.HasPrefix(id, "") || id == "" {
		t.Fatalf("unexpected id: %q", id)
	}

	stdout, stderr, code = runCLI(t, dir, nil, "show", id)
	if code != 0 {
		t.Fatalf("show failed: %s", stderr)
	}
	if !strings.Contains(stdout, "Fix login timeout") {
		t.Fatalf("show output missing title:\n%s", stdout)
	}
}

func TestCLIInitDir(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "project")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runCLI(t, root, nil, "init", "--dir", target, "--prefix", "orca")
	if code != 0 {
		t.Fatalf("init --dir failed: %s", stderr)
	}

	if _, err := os.Stat(filepath.Join(target, ".tq", "config.json")); err != nil {
		t.Fatalf("expected workspace at target dir: %v", err)
	}
}

func TestCLIReadyAndShowJSON(t *testing.T) {
	dir := mustInitCLIWorkspace(t)

	stdout, stderr, code := runCLI(t, dir, nil, "create", "Task A")
	if code != 0 {
		t.Fatalf("create failed: %s", stderr)
	}
	id := strings.TrimSpace(stdout)

	stdout, stderr, code = runCLI(t, dir, nil, "ready", "--json")
	if code != 0 {
		t.Fatalf("ready --json failed: %s", stderr)
	}
	var ready []*Issue
	if err := json.Unmarshal([]byte(stdout), &ready); err != nil {
		t.Fatalf("ready output is not json: %v\n%s", err, stdout)
	}
	if len(ready) != 1 || ready[0].ID != id {
		t.Fatalf("unexpected ready issues: %+v", ready)
	}

	stdout, stderr, code = runCLI(t, dir, nil, "show", id, "--json")
	if code != 0 {
		t.Fatalf("show --json failed: %s", stderr)
	}
	var issue Issue
	if err := json.Unmarshal([]byte(stdout), &issue); err != nil {
		t.Fatalf("show output is not json: %v\n%s", err, stdout)
	}
	if issue.ID != id {
		t.Fatalf("expected id %q, got %q", id, issue.ID)
	}
}

func TestCLIClaimCases(t *testing.T) {
	t.Run("already claimed", func(t *testing.T) {
		dir := mustInitCLIWorkspace(t)
		stdout, stderr, code := runCLI(t, dir, nil, "create", "Task A")
		if code != 0 {
			t.Fatalf("create failed: %s", stderr)
		}
		id := strings.TrimSpace(stdout)
		_, stderr, code = runCLI(t, dir, nil, "claim", id, "--actor", "agent-1")
		if code != 0 {
			t.Fatalf("first claim failed: %s", stderr)
		}
		_, stderr, code = runCLI(t, dir, nil, "claim", id, "--actor", "agent-2")
		if code == 0 {
			t.Fatal("expected second claim to fail")
		}
		if !strings.Contains(stderr, "issue is not open") {
			t.Fatalf("expected already-claimed message, got: %s", stderr)
		}
	})

	t.Run("blocked", func(t *testing.T) {
		dir := mustInitCLIWorkspace(t)
		bOut, bErr, bCode := runCLI(t, dir, nil, "create", "Blocker")
		if bCode != 0 {
			t.Fatalf("create blocker failed: %s", bErr)
		}
		blockerID := strings.TrimSpace(bOut)
		tOut, tErr, tCode := runCLI(t, dir, nil, "create", "Target")
		if tCode != 0 {
			t.Fatalf("create target failed: %s", tErr)
		}
		targetID := strings.TrimSpace(tOut)
		_, stderr, code := runCLI(t, dir, nil, "dep", "add", targetID, blockerID)
		if code != 0 {
			t.Fatalf("dep add failed: %s", stderr)
		}

		_, stderr, code = runCLI(t, dir, nil, "claim", targetID, "--actor", "agent-1")
		if code == 0 {
			t.Fatal("expected claim to fail for blocked issue")
		}
		if !strings.Contains(stderr, "issue is blocked") {
			t.Fatalf("expected blocked message, got: %s", stderr)
		}
	})
}

func TestCLIInvalidStatusOps(t *testing.T) {
	dir := mustInitCLIWorkspace(t)
	stdout, stderr, code := runCLI(t, dir, nil, "create", "Task A")
	if code != 0 {
		t.Fatalf("create failed: %s", stderr)
	}
	id := strings.TrimSpace(stdout)

	_, stderr, code = runCLI(t, dir, nil, "update", id, "--status", "in_progress")
	if code == 0 {
		t.Fatal("expected update --status in_progress to fail")
	}
	_, stderr, code = runCLI(t, dir, nil, "update", id, "--status", "closed")
	if code == 0 {
		t.Fatal("expected update --status closed to fail")
	}

	_, stderr, code = runCLI(t, dir, nil, "close", id)
	if code != 0 {
		t.Fatalf("close failed: %s", stderr)
	}
	_, stderr, code = runCLI(t, dir, nil, "close", id)
	if code == 0 {
		t.Fatal("expected second close to fail")
	}

	_, stderr, code = runCLI(t, dir, nil, "reopen", id)
	if code != 0 {
		t.Fatalf("reopen closed issue failed: %s", stderr)
	}
	_, stderr, code = runCLI(t, dir, nil, "reopen", id)
	if code == 0 {
		t.Fatal("expected reopen non-closed to fail")
	}
}

func TestCLIDepAndDoctorJSON(t *testing.T) {
	dir := mustInitCLIWorkspace(t)
	aOut, aErr, aCode := runCLI(t, dir, nil, "create", "A")
	if aCode != 0 {
		t.Fatalf("create A failed: %s", aErr)
	}
	bOut, bErr, bCode := runCLI(t, dir, nil, "create", "B")
	if bCode != 0 {
		t.Fatalf("create B failed: %s", bErr)
	}
	aID := strings.TrimSpace(aOut)
	bID := strings.TrimSpace(bOut)

	_, stderr, code := runCLI(t, dir, nil, "dep", "add", aID, bID)
	if code != 0 {
		t.Fatalf("dep add failed: %s", stderr)
	}

	stdout, stderr, code := runCLI(t, dir, nil, "dep", "list", aID, "--json")
	if code != 0 {
		t.Fatalf("dep list --json failed: %s", stderr)
	}
	var graph map[string]any
	if err := json.Unmarshal([]byte(stdout), &graph); err != nil {
		t.Fatalf("dep list output is not json: %v\n%s", err, stdout)
	}
	if _, ok := graph["blocked_by"]; !ok {
		t.Fatalf("dep list json missing blocked_by: %v", graph)
	}
	if _, ok := graph["blocks"]; !ok {
		t.Fatalf("dep list json missing blocks: %v", graph)
	}

	stdout, stderr, code = runCLI(t, dir, nil, "doctor", "--json")
	if code != 0 {
		t.Fatalf("doctor --json failed: %s", stderr)
	}
	var report map[string]any
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("doctor output is not json: %v\n%s", err, stdout)
	}
	checksRaw, ok := report["checks"].([]any)
	if !ok || len(checksRaw) == 0 {
		t.Fatalf("doctor json checks missing: %v", report)
	}
	firstCheck, ok := checksRaw[0].(map[string]any)
	if !ok {
		t.Fatalf("doctor check malformed: %v", checksRaw[0])
	}
	if _, ok := firstCheck["id"]; !ok {
		t.Fatalf("doctor check missing id: %v", firstCheck)
	}
	if _, ok := firstCheck["name"]; !ok {
		t.Fatalf("doctor check missing name: %v", firstCheck)
	}
}

func TestCLIPartialIDAmbiguous(t *testing.T) {
	dir := mustInitCLIWorkspace(t)

	// force known IDs with shared suffix prefix.
	issue1 := &Issue{ID: "orca-a1b2", Title: "A", Status: StatusOpen, Priority: 2, CreatedAt: "2026-03-22T10:00:00Z", UpdatedAt: "2026-03-22T10:00:00Z"}
	issue2 := &Issue{ID: "orca-a1b9", Title: "B", Status: StatusOpen, Priority: 2, CreatedAt: "2026-03-22T10:05:00Z", UpdatedAt: "2026-03-22T10:05:00Z"}
	for _, issue := range []*Issue{issue1, issue2} {
		data, err := json.MarshalIndent(issue, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		data = append(data, '\n')
		if err := os.WriteFile(filepath.Join(dir, ".tq", "issues", issue.ID+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	_, stderr, code := runCLI(t, dir, nil, "show", "a1b")
	if code == 0 {
		t.Fatal("expected ambiguous partial id to fail")
	}
	if !strings.Contains(stderr, "ambiguous") {
		t.Fatalf("expected ambiguous error, got: %s", stderr)
	}
}

func TestCLIDiscoveryOverride(t *testing.T) {
	dir := mustInitCLIWorkspace(t)
	other := t.TempDir()

	// --dir should work from unrelated cwd.
	stdout, stderr, code := runCLI(t, other, nil, "create", "Task A", "--dir", dir)
	if code != 0 {
		t.Fatalf("create with --dir failed: %s", stderr)
	}
	id := strings.TrimSpace(stdout)
	if id == "" {
		t.Fatal("expected id output")
	}

	// TQ_DIR should work from unrelated cwd.
	stdout, stderr, code = runCLI(t, other, map[string]string{"TQ_DIR": dir}, "list", "--json")
	if code != 0 {
		t.Fatalf("list with TQ_DIR failed: %s", stderr)
	}
	var issues []*Issue
	if err := json.Unmarshal([]byte(stdout), &issues); err != nil {
		t.Fatalf("list output not json: %v\n%s", err, stdout)
	}
	if len(issues) == 0 {
		t.Fatal("expected at least one issue")
	}
}

func TestCLIMalformedWarningPreservesJSON(t *testing.T) {
	dir := mustInitCLIWorkspace(t)
	_, stderr, code := runCLI(t, dir, nil, "create", "Task A")
	if code != 0 {
		t.Fatalf("create failed: %s", stderr)
	}

	badPath := filepath.Join(dir, ".tq", "issues", "orca-bad1.json")
	if err := os.WriteFile(badPath, []byte("{not json}"), 0o644); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, code := runCLI(t, dir, nil, "ready", "--json")
	if code != 0 {
		t.Fatalf("ready --json failed: %s", stderr)
	}
	if !strings.Contains(stderr, "warning") {
		t.Fatalf("expected warning on stderr, got: %s", stderr)
	}
	var issues []*Issue
	if err := json.Unmarshal([]byte(stdout), &issues); err != nil {
		t.Fatalf("stdout must remain valid json: %v\n%s", err, stdout)
	}
}
