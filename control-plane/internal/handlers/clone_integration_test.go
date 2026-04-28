//go:build docker_integration

package handlers_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// pollCloneTaskTerminal polls the tasks API for an instance.clone task tied
// to the given destination instance until it reaches a terminal state.
// Returns the final state and message; the test asserts on these.
func pollCloneTaskTerminal(t *testing.T, client *http.Client, baseURL string, instanceID uint, timeout time.Duration) (string, string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/tasks?type=instance.clone&instance_id=%d", baseURL, instanceID))
		if err != nil {
			time.Sleep(1 * time.Second)
			continue
		}
		var tasks []struct {
			ID      string `json:"id"`
			State   string `json:"state"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&tasks)
		resp.Body.Close()
		for _, task := range tasks {
			if task.State == "succeeded" || task.State == "failed" || task.State == "canceled" {
				return task.State, task.Message
			}
		}
		time.Sleep(1 * time.Second)
	}
	t.Fatalf("clone task for instance %d did not reach terminal state within %s", instanceID, timeout)
	return "", ""
}

// pollInstanceRunning polls /api/v1/instances/{id} until status=running.
func pollInstanceRunning(t *testing.T, client *http.Client, baseURL string, instanceID uint, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(fmt.Sprintf("%s/api/v1/instances/%d", baseURL, instanceID))
		if err != nil {
			time.Sleep(2 * time.Second)
			continue
		}
		var poll map[string]interface{}
		_ = json.NewDecoder(resp.Body).Decode(&poll)
		resp.Body.Close()
		if status, _ := poll["status"].(string); status == "running" {
			return
		} else if status == "error" {
			t.Fatalf("instance %d entered error status: %v", instanceID, poll["status_message"])
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("instance %d did not reach 'running' within %s", instanceID, timeout)
}

// createInstance posts /api/v1/instances and returns (id, k8s-safe name).
func createInstance(t *testing.T, client *http.Client, baseURL, displayName string) (uint, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"display_name": displayName})
	resp, err := client.Post(baseURL+"/api/v1/instances", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create instance %q: %v", displayName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		bs, _ := io.ReadAll(resp.Body)
		t.Fatalf("create instance %q: status %d body=%s", displayName, resp.StatusCode, bs)
	}
	var r struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&r)
	return r.ID, r.Name
}

// deleteInstance is the standard test cleanup — best-effort, never fails the
// test (avoids masking the real failure).
func deleteInstance(t *testing.T, client *http.Client, baseURL string, id uint, name string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/api/v1/instances/%d", baseURL, id), nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Logf("cleanup: delete instance %d: %v", id, err)
		return
	}
	resp.Body.Close()
	// Belt-and-suspenders: kill the browser container if Delete didn't.
	_ = exec.Command("docker", "rm", "-f", name+"-browser").Run()
}

// TestIntegration_Clone_CarriesBrowserVolume_AndScrubsSingletons is the
// load-bearing end-to-end test for the recent clone fixes:
//
//  1. Browser data (Chrome profile, cookies, persisted state) survives a
//     clone — we drop a sentinel file in src's profile and confirm it
//     shows up in the cloned instance.
//  2. Chromium's host-scoped Singleton{Lock,Cookie,Socket} files are
//     stripped from the destination volume. Without that scrub, the
//     cloned instance's Chromium would abort every launch with
//     "profile appears to be in use" and the dst browser pod would never
//     reach state=running.
//
// If either fix regresses, this test fails: missing sentinel proves the
// volume copy didn't run, and a cloned instance whose browser pod never
// becomes "running" proves the Singleton scrub didn't run.
func TestIntegration_Clone_CarriesBrowserVolume_AndScrubsSingletons(t *testing.T) {
	baseURL := sessionURL
	client := &http.Client{Timeout: 60 * time.Second}

	// --- Source instance: create + wait running + start browser ---
	srcID, srcName := createInstance(t, client, baseURL, fmt.Sprintf("clone-src-%d", time.Now().UnixNano()))
	defer deleteInstance(t, client, baseURL, srcID, srcName)
	t.Logf("src instance id=%d name=%s", srcID, srcName)
	pollInstanceRunning(t, client, baseURL, srcID, 120*time.Second)

	resp, err := client.Post(fmt.Sprintf("%s/api/v1/instances/%d/browser/start", baseURL, srcID), "application/json", nil)
	if err != nil {
		t.Fatalf("src browser/start: %v", err)
	}
	resp.Body.Close()
	srcBrowser := srcName + "-browser"
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if pollBrowserState(t, client, baseURL, srcID) == "running" {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if pollBrowserState(t, client, baseURL, srcID) != "running" {
		t.Fatalf("src browser did not reach running")
	}
	t.Logf("src browser %s running ✓", srcBrowser)

	// Plant deterministic state inside src's chrome-data:
	//   - a sentinel file to prove the volume copy ran
	//   - the three Singleton{Lock,Cookie,Socket} symlinks Chromium creates
	//     when it boots; we forge them so the scrub assertion below is
	//     deterministic regardless of Chromium's actual launch timing.
	sentinel := fmt.Sprintf("clone-test-%d", time.Now().UnixNano())
	plant := fmt.Sprintf(`set -e
echo %s > /home/claworc/chrome-data/sentinel.txt
ln -sf "$(hostname)-99" /home/claworc/chrome-data/SingletonLock
ln -sf 1234567890 /home/claworc/chrome-data/SingletonCookie
ln -sf /tmp/org.chromium.Chromium.fake/SingletonSocket /home/claworc/chrome-data/SingletonSocket`, sentinel)
	if out, err := exec.Command("docker", "exec", srcBrowser, "sh", "-c", plant).CombinedOutput(); err != nil {
		t.Fatalf("plant sentinel + Singleton files in src: %v (%s)", err, out)
	}

	// Stop src's browser so its volume isn't being held when the clone copy
	// runs. Stop is enough — the volume isn't deleted until DeleteBrowserPod.
	resp, err = client.Post(fmt.Sprintf("%s/api/v1/instances/%d/browser/stop", baseURL, srcID), "application/json", nil)
	if err != nil {
		t.Fatalf("src browser/stop: %v", err)
	}
	resp.Body.Close()
	t.Log("src browser stopped; ready to clone")

	// --- Clone src ---
	resp, err = client.Post(fmt.Sprintf("%s/api/v1/instances/%d/clone", baseURL, srcID), "application/json", nil)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		bs, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("clone: status %d body=%s", resp.StatusCode, bs)
	}
	var dstResp struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&dstResp)
	resp.Body.Close()
	defer deleteInstance(t, client, baseURL, dstResp.ID, dstResp.Name)
	t.Logf("dst instance id=%d name=%s", dstResp.ID, dstResp.Name)

	state, msg := pollCloneTaskTerminal(t, client, baseURL, dstResp.ID, 180*time.Second)
	if state != "succeeded" {
		t.Fatalf("clone task state=%s message=%s, want succeeded", state, msg)
	}

	// --- Inspect the dst volume directly (BEFORE starting dst's browser) to
	//     confirm the scrub stripped the host-scoped Singleton files. We
	//     mount the live volume into a transient alpine container; once
	//     dst's Chromium launches it will recreate SingletonLock for its
	//     own process, so this is the only window where the post-scrub
	//     state is observable. ---
	dstVol := "claworc-" + dstResp.Name + "-browser"
	out, err := exec.Command("docker", "run", "--rm", "-v", dstVol+":/vol", "alpine",
		"sh", "-c", "ls /vol/sentinel.txt; ! test -e /vol/SingletonLock && ! test -e /vol/SingletonCookie && ! test -e /vol/SingletonSocket && echo SCRUB_OK").CombinedOutput()
	if err != nil {
		t.Fatalf("inspect dst volume: %v (%s)", err, out)
	}
	body := string(out)
	if !strings.Contains(body, "/vol/sentinel.txt") {
		t.Errorf("sentinel missing from dst volume — copy did not run; output:\n%s", body)
	}
	if !strings.Contains(body, "SCRUB_OK") {
		t.Errorf("Singleton files still present in dst volume — scrub did not run; output:\n%s", body)
	}

	// --- Sentinel content matches src's (volume contents preserved). ---
	out, err = exec.Command("docker", "run", "--rm", "-v", dstVol+":/vol", "alpine",
		"cat", "/vol/sentinel.txt").CombinedOutput()
	if err != nil {
		t.Fatalf("read sentinel from dst volume: %v (%s)", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != sentinel {
		t.Errorf("sentinel mismatch: got %q, want %q", got, sentinel)
	}
	t.Log("dst volume: sentinel preserved + Singleton files scrubbed ✓")

	// --- Spin up dst's browser. If the Singleton scrub didn't run, Chromium
	//     would refuse to launch and the pod would never reach running. ---
	resp, err = client.Post(fmt.Sprintf("%s/api/v1/instances/%d/browser/start", baseURL, dstResp.ID), "application/json", nil)
	if err != nil {
		t.Fatalf("dst browser/start: %v", err)
	}
	resp.Body.Close()
	dstBrowser := dstResp.Name + "-browser"
	deadline = time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if pollBrowserState(t, client, baseURL, dstResp.ID) == "running" {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if pollBrowserState(t, client, baseURL, dstResp.ID) != "running" {
		t.Fatalf("dst browser did not reach running — Singleton scrub likely regressed")
	}
	t.Logf("dst browser %s running ✓ (proves Chromium booted on the cloned profile)", dstBrowser)
}

// TestIntegration_Clone_NoOpBrowserVolume_WhenSrcNeverLaunched pins the
// idempotent branch in DockerOrchestrator.CloneBrowserVolume: when the
// source has never spawned a browser pod, the destination's browser
// volume is not created. Otherwise the orchestrator would emit confusing
// "no source volume to copy from" errors and pollute the test environment
// with empty volumes.
func TestIntegration_Clone_NoOpBrowserVolume_WhenSrcNeverLaunched(t *testing.T) {
	baseURL := sessionURL
	client := &http.Client{Timeout: 60 * time.Second}

	srcID, srcName := createInstance(t, client, baseURL, fmt.Sprintf("clone-noop-%d", time.Now().UnixNano()))
	defer deleteInstance(t, client, baseURL, srcID, srcName)
	pollInstanceRunning(t, client, baseURL, srcID, 120*time.Second)

	// Sanity: src has no browser volume yet.
	if err := exec.Command("docker", "volume", "inspect", "claworc-"+srcName+"-browser").Run(); err == nil {
		t.Fatalf("precondition failed: src already has a browser volume")
	}

	resp, err := client.Post(fmt.Sprintf("%s/api/v1/instances/%d/clone", baseURL, srcID), "application/json", nil)
	if err != nil {
		t.Fatalf("clone: %v", err)
	}
	if resp.StatusCode != http.StatusCreated {
		bs, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("clone: status %d body=%s", resp.StatusCode, bs)
	}
	var dstResp struct {
		ID   uint   `json:"id"`
		Name string `json:"name"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&dstResp)
	resp.Body.Close()
	defer deleteInstance(t, client, baseURL, dstResp.ID, dstResp.Name)

	state, msg := pollCloneTaskTerminal(t, client, baseURL, dstResp.ID, 180*time.Second)
	if state != "succeeded" {
		t.Fatalf("clone task state=%s message=%s, want succeeded", state, msg)
	}

	// dst must NOT have a browser volume — the no-op branch should have
	// skipped creation entirely.
	if err := exec.Command("docker", "volume", "inspect", "claworc-"+dstResp.Name+"-browser").Run(); err == nil {
		t.Errorf("dst browser volume was created despite src having none")
	}
}
