package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Real child processes expose retry bugs hidden by in-process counters.
func TestDeliveryCLIChildProcess(t *testing.T) {
	if os.Getenv("WORKFLOW_DELIVERY_TEST_CHILD") != "1" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("WORKFLOW_DELIVERY_TEST_ARGS")), &args); err != nil {
		panic(err)
	}
	os.Exit(run(args))
}

func childCLI(t *testing.T, want int, args ...string) string {
	t.Helper()
	raw, _ := json.Marshal(args)
	cmd := exec.Command(os.Args[0], "-test.run=^TestDeliveryCLIChildProcess$")
	cmd.Env = append(os.Environ(), "WORKFLOW_DELIVERY_TEST_CHILD=1", "WORKFLOW_DELIVERY_TEST_ARGS="+string(raw))
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	if code != want {
		t.Fatalf("CLI %v: exit=%d want=%d\n%s", args, code, want, out)
	}
	return string(out)
}

func TestProgramFailedDeliveryRetryAcrossProcesses(t *testing.T) {
	r, base, final := setupCLIProgram(t)
	args := []string{"execute", "program", "prog-1", "--root", r.host, "--source-branch", "integration/prog-1", "--source-sha", final, "--parent-branch", "develop", "--test"}
	childCLI(t, 1, append(args, "false")...)
	if r.remoteSHA("develop") != base {
		t.Fatal("failed tests updated parent")
	}
	childCLI(t, 0, append(args, "test -f internal/t1.go")...)
	if r.remoteSHA("develop") != final {
		t.Fatal("second process did not deliver")
	}
	if refs := cliGit(t, r.host, "branch", "--list", "scratch/merger-*"); refs != "" {
		t.Fatalf("scratch branches leaked: %s", refs)
	}
}

func taskFixture(t *testing.T, kind string) (*cliRepo, map[string]any, string, string) {
	t.Helper()
	r := newCLIRepo(t)
	cliGit(t, r.host, "checkout", "-qb", "develop")
	r.write("README.md", "TEST FIXTURE ONLY\n")
	r.write(".agent/workflow.yaml", "schema_version: 1\ngit:\n  integration_branch: develop\nresources: {}\n")
	base := r.commit("baseline")
	r.push("develop")
	cliGit(t, r.host, "checkout", "-qb", "feature/task")
	r.write("price.go", "package price\n")
	r.write(".agent/tasks/task/task.md", "# TEST FIXTURE\nGoal: test controlled delivery.\n")
	contract := map[string]any{"status": "NOT_REQUIRED", "target": ""}
	if kind == "COMPLEX" {
		r.write(".agent/tasks/task/contract.md", "# TEST FIXTURE approved contract\n")
	}
	evidence := r.commit("implementation evidence")
	if kind == "COMPLEX" {
		contract = map[string]any{"status": "APPROVED", "target": evidence, "revision": 1}
	}
	s := map[string]any{
		"schema_version": 4, "task_id": "task", "task_type": kind,
		"git":      map[string]any{"parent_branch": "develop", "base_commit": base, "task_branch": "feature/task"},
		"contract": contract, "resources": map[string]any{"reservations": map[string]any{}},
		"review":   map[string]any{"status": "CLEAN", "target": evidence},
		"owner":    map[string]any{"status": "ACCEPTED", "review_target": evidence},
		"delivery": map[string]any{"status": "NOT_RUN", "review_target": "", "feature_head": "", "develop_base": ""},
		"blocked":  map[string]any{"active": false, "by": "", "reason": ""},
		"handoff": map[string]any{
			"prepared": true, "scope": "task", "scope_id": "task", "workflow": kind,
			"next_role": "Merger", "source_commit": evidence, "next_action": "TEST FIXTURE delivery", "entry": "delivery-start",
			"evidence": map[string]any{"version": 1, "items": []any{map[string]any{"kind": "task", "sha": evidence, "path": ".agent/tasks/task/task.md"}}},
			"ack":      map[string]any{"version": 1, "acked_by": "Merger", "target": evidence},
		},
	}
	writeTaskState(r, s)
	r.commit("TEST FIXTURE review, acceptance and handoff")
	r.push("feature/task")
	return r, s, base, evidence
}

func writeTaskState(r *cliRepo, s map[string]any) {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		r.t.Fatal(err)
	}
	r.write(".agent/tasks/task/state.yaml", string(raw)+"\n")
}

func withoutDelivery(t *testing.T, r *cliRepo) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.host, ".agent/tasks/task/state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "delivery")
	return m
}

func TestTaskDeliveryLifecycleAcrossProcesses(t *testing.T) {
	for _, kind := range []string{"NORMAL", "COMPLEX"} {
		t.Run(kind, func(t *testing.T) {
			r, _, base, evidence := taskFixture(t, kind)
			// Existing legacy scratch refs belong to someone else and must survive.
			legacy := "scratch/merger-" + base[:8] + "-1"
			cliGit(t, r.host, "branch", legacy, base)
			childCLI(t, 0, "plan", "task", "task", "--root", r.host)
			if cliGit(t, r.host, "status", "--porcelain") != "" {
				t.Fatal("plan wrote artifacts")
			}
			// Cannot execute without delivery PASS.
			tip := cliGit(t, r.host, "rev-parse", "HEAD")
			childCLI(t, 1, "execute", "task", "task", "--root", r.host, "--source-sha", tip, "--parent-branch", "develop", "--test", "true")
			childCLI(t, 1, "verify", "task", "task", "--root", r.host, "--test", "false")
			if cliGit(t, r.host, "status", "--porcelain") != "" || r.remoteSHA("develop") != base {
				t.Fatal("failed verification wrote PASS or updated parent")
			}
			before := withoutDelivery(t, r)
			childCLI(t, 0, "verify", "task", "task", "--root", r.host, "--test", "test -f price.go")
			if !reflect.DeepEqual(before, withoutDelivery(t, r)) {
				t.Fatal("verify changed non-delivery state")
			}
			if r.remoteSHA("develop") != base {
				t.Fatal("verify updated parent")
			}
			// Before metadata is committed/pushed it must not be executable.
			childCLI(t, 1, "execute", "task", "task", "--root", r.host, "--source-sha", tip, "--parent-branch", "develop", "--test", "true")
			tip = r.commit("delivery-neutral metadata")
			r.push("feature/task")
			// Review evidence alone is insufficient authorization for the actual tip.
			childCLI(t, 1, "execute", "task", "task", "--root", r.host, "--source-sha", evidence, "--parent-branch", "develop", "--test", "true")
			childCLI(t, 1, "execute", "task", "task", "--root", r.host, "--source-sha", tip, "--parent-branch", "main", "--test", "true")
			childCLI(t, 0, "execute", "task", "task", "--root", r.host, "--source-sha", tip, "--parent-branch", "develop", "--test", "test -f price.go")
			if r.remoteSHA("develop") != tip {
				t.Fatal("execute did not deliver exact source")
			}
			if got := cliGit(t, r.host, "branch", "--list", "scratch/merger-*"); strings.TrimSpace(got) != legacy {
				t.Fatalf("deleted foreign refs or leaked own refs: %s", got)
			}
		})
	}
}

func TestTaskExecuteRejectsDriftAndProgramChild(t *testing.T) {
	for _, scenario := range []string{"parent", "source", "neutral-but-not-delivery", "program"} {
		t.Run(scenario, func(t *testing.T) {
			r, s, base, _ := taskFixture(t, "NORMAL")
			if scenario == "program" {
				s["program"] = map[string]any{"id": "prog", "version": 1}
				writeTaskState(r, s)
				r.commit("program membership")
				r.push("feature/task")
			}
			childCLI(t, 0, "verify", "task", "task", "--root", r.host, "--test", "true")
			tip := r.commit("delivery metadata")
			r.push("feature/task")
			expectedParent := base
			switch scenario {
			case "parent":
				cliGit(t, r.host, "checkout", "develop")
				r.write("README.md", "advanced\n")
				expectedParent = r.commit("parent drift")
				r.push("develop")
				cliGit(t, r.host, "checkout", "feature/task")
			case "source":
				r.write("price.go", "package price\n// substantive drift\n")
				tip = r.commit("source drift")
				r.push("feature/task")
			case "neutral-but-not-delivery":
				r.write(".agent/tasks/task/findings.md", "review-neutral but invalidates delivery\n")
				tip = r.commit("non-delivery tail")
				r.push("feature/task")
			}
			childCLI(t, 1, "execute", "task", "task", "--root", r.host, "--source-sha", tip, "--parent-branch", "develop", "--test", "true")
			if r.remoteSHA("develop") != expectedParent {
				t.Fatal("invalid delivery changed parent")
			}
		})
	}
}

func TestTaskDeliveryRejectsSymlinkAndTraversal(t *testing.T) {
	r, _, base, _ := taskFixture(t, "NORMAL")
	external := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(external, []byte("preserve\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(r.host, ".agent/tasks/task/delivery.md")); err != nil {
		t.Fatal(err)
	}
	r.commit("symlink fixture")
	r.push("feature/task")
	childCLI(t, 2, "verify", "task", "task", "--root", r.host, "--test", "true")
	childCLI(t, 2, "verify", "task", "../task", "--root", r.host, "--test", "true")
	raw, _ := os.ReadFile(external)
	if string(raw) != "preserve\n" || r.remoteSHA("develop") != base {
		t.Fatal("symlink/traversal wrote external data")
	}
}

func TestTaskVerifyRejectsRemoteChangesDuringTests(t *testing.T) {
	for _, branch := range []string{"develop", "feature/task"} {
		t.Run(branch, func(t *testing.T) {
			r, _, base, _ := taskFixture(t, "NORMAL")
			peer := filepath.Join(t.TempDir(), "peer")
			cliGit(t, r.host, "clone", "-q", "--branch", branch, r.remoteURL(), peer)
			cliGit(t, peer, "config", "user.name", "test")
			cliGit(t, peer, "config", "user.email", "test@example.com")
			if err := os.WriteFile(filepath.Join(peer, "README.md"), []byte("remote advance\n"), 0600); err != nil {
				t.Fatal(err)
			}
			cliGit(t, peer, "add", "README.md")
			cliGit(t, peer, "commit", "-qm", "remote drift prepared")
			// During the isolated test advance only the live remote; local cached
			// refs remain unchanged, so the final ls-remote check is essential.
			script := filepath.Join(t.TempDir(), "advance.sh")
			body := "#!/bin/sh\nset -eu\ngit -C '" + strings.ReplaceAll(peer, "'", "'\\''") + "' push -q origin HEAD\n"
			if err := os.WriteFile(script, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			out := childCLI(t, 1, "verify", "task", "task", "--root", r.host, "--test", "sh "+script)
			if !strings.Contains(out, "变化") && !strings.Contains(out, "漂移") && !strings.Contains(out, "Freshness") {
				t.Fatalf("wrong refusal: %s", out)
			}
			if cliGit(t, r.host, "status", "--porcelain") != "" {
				t.Fatal("stale validation wrote PASS")
			}
			if branch != "develop" && r.remoteSHA("develop") != base {
				t.Fatal("source drift updated parent")
			}
		})
	}
}

func (r *cliRepo) remoteURL() string {
	return cliGit(r.t, r.host, "remote", "get-url", "origin")
}

func TestTaskDeliveryRejectsInvalidInputs(t *testing.T) {
	for _, scenario := range []string{"owner", "review", "blocked", "handoff", "branch", "contract", "design", "legacy", "config"} {
		t.Run(scenario, func(t *testing.T) {
			r, s, base, _ := taskFixture(t, "NORMAL")
			want := 1
			switch scenario {
			case "owner":
				s["owner"].(map[string]any)["status"] = "PENDING"
			case "review":
				s["review"].(map[string]any)["status"] = "PENDING"
			case "blocked":
				s["blocked"].(map[string]any)["active"] = true
			case "handoff":
				s["handoff"].(map[string]any)["ack"] = nil
			case "branch":
				s["git"].(map[string]any)["task_branch"] = "feature/other"
			case "contract":
				s["task_type"] = "COMPLEX"
			case "design":
				s["task_type"] = "DESIGN"
			case "legacy":
				s = map[string]any{"schema_version": 3, "task_id": "task"}
			case "config":
				r.write(".agent/workflow.yaml", "invalid: true\n")
				want = 2
			}
			writeTaskState(r, s)
			r.commit("invalid input fixture")
			r.push("feature/task")
			childCLI(t, want, "verify", "task", "task", "--root", r.host, "--test", "true")
			if r.remoteSHA("develop") != base {
				t.Fatal("invalid verification updated parent")
			}
		})
	}
}
