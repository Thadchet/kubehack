//go:build integration

package api

import (
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"kubehack/internal/helm"
)

// These use a real published chart. Upgrading between two of its versions is the
// part a chart written for the test could not show.
const (
	helmNamespace = "kh-helm"
	podinfoOCI    = "oci://ghcr.io/stefanprodan/charts/podinfo"
	podinfoRepo   = "https://stefanprodan.github.io/podinfo"
	podinfoOld    = "6.7.0"
	podinfoNew    = "6.8.0"
)

func cleanHelmNamespace(t *testing.T) {
	t.Helper()
	remove := func() {
		exec.Command("kubectl", "--context", kindContext, "delete", "namespace", helmNamespace,
			"--ignore-not-found", "--wait=true", "--timeout=90s").Run()
	}
	remove()
	t.Cleanup(remove)
}

func helmCLI(t *testing.T, args ...string) string {
	t.Helper()
	all := append([]string{"--kube-context", kindContext, "-n", helmNamespace}, args...)
	output, err := exec.Command("helm", all...).CombinedOutput()
	if err != nil {
		t.Fatalf("helm %v: %v\n%s", args, err, output)
	}
	return string(output)
}

func helmPost(t *testing.T, server *Server, action string, body any, response any) (int, string) {
	t.Helper()
	content, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost,
		server.BaseURL()+"/api/v1/contexts/"+kindContext+"/helm/"+action, strings.NewReader(string(content)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+server.Token())
	reply, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer reply.Body.Close()
	text, err := io.ReadAll(reply.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response != nil && reply.StatusCode == http.StatusOK {
		if err := json.Unmarshal(text, response); err != nil {
			t.Fatal(err)
		}
	}
	return reply.StatusCode, string(text)
}

func mustHelm(t *testing.T, server *Server, action string, body any, response any) {
	t.Helper()
	if status, text := helmPost(t, server, action, body, response); status != http.StatusOK {
		t.Fatalf("%s: status = %d: %s", action, status, text)
	}
}

func helmGet(t *testing.T, server *Server, path string, response any) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, server.BaseURL()+"/api/v1/contexts/"+kindContext+"/helm/"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+server.Token())
	reply, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer reply.Body.Close()
	if reply.StatusCode == http.StatusOK {
		if err := json.NewDecoder(reply.Body).Decode(response); err != nil {
			t.Fatal(err)
		}
	}
	return reply.StatusCode
}

func podinfoReplicas(t *testing.T, name string) string {
	t.Helper()
	return kubectl(t, "", "-n", helmNamespace, "get", "deployment", name+"-podinfo", "-o", "jsonpath={.spec.replicas}")
}

func TestKindHelmFullCycleWithRealChart(t *testing.T) {
	cleanHelmNamespace(t)
	server := startKindServer(t)
	install := helm.Change{
		Namespace: helmNamespace, Name: "web", Chart: podinfoOCI, Version: podinfoOld,
		Values: "replicaCount: 1\n", CreateNamespace: true, Wait: true,
	}

	// A preview needs the namespace to be left uncreated, and nothing installed.
	preview := install
	preview.DryRun = true
	var planned helm.Result
	mustHelm(t, server, "install", preview, &planned)
	if !strings.Contains(planned.Manifest, "kind: Deployment") {
		t.Fatalf("manifest = %s", planned.Manifest)
	}
	if output, _ := exec.Command("kubectl", "--context", kindContext, "get", "namespace", helmNamespace).CombinedOutput(); !strings.Contains(string(output), "NotFound") {
		t.Fatalf("dry run touched the cluster: %s", output)
	}

	var installed helm.Result
	mustHelm(t, server, "install", install, &installed)
	if installed.Release.Status != "deployed" || installed.Release.ChartVersion != podinfoOld {
		t.Fatalf("release = %+v", installed.Release)
	}
	if ready := kubectl(t, "", "-n", helmNamespace, "get", "deployment", "web-podinfo", "-o", "jsonpath={.status.readyReplicas}"); ready != "1" {
		t.Fatalf("wait returned with %q ready", ready)
	}
	if listed := helmCLI(t, "list", "-q"); strings.TrimSpace(listed) != "web" {
		t.Fatalf("helm list = %q", listed)
	}

	var values map[string]string
	if status := helmGet(t, server, "releases/"+helmNamespace+"/web/values", &values); status != http.StatusOK {
		t.Fatalf("values status = %d", status)
	}
	if values["values"] != "replicaCount: 1\n" {
		t.Fatalf("values = %q", values["values"])
	}

	// Values only, keeping the chart in the release.
	mustHelm(t, server, "upgrade", helm.Change{
		Namespace: helmNamespace, Name: "web", Values: "replicaCount: 2\n", ExpectedRevision: 1,
	}, nil)
	if replicas := podinfoReplicas(t, "web"); replicas != "2" {
		t.Fatalf("replicas = %s", replicas)
	}

	// A new chart version, previewed against what is running.
	change := helm.Change{
		Namespace: helmNamespace, Name: "web", Chart: podinfoOCI, Version: podinfoNew,
		Values: "replicaCount: 2\n", ExpectedRevision: 2, DryRun: true,
	}
	var diff helm.Result
	mustHelm(t, server, "upgrade", change, &diff)
	if !strings.Contains(diff.Current, "helm.sh/chart: podinfo-"+podinfoOld) ||
		!strings.Contains(diff.Manifest, "helm.sh/chart: podinfo-"+podinfoNew) {
		t.Fatal("preview does not show the version change")
	}
	change.DryRun = false
	mustHelm(t, server, "upgrade", change, nil)

	var detail helm.Detail
	if status := helmGet(t, server, "releases/"+helmNamespace+"/web", &detail); status != http.StatusOK {
		t.Fatalf("detail status = %d", status)
	}
	if detail.Revision != 3 || detail.ChartVersion != podinfoNew || len(detail.History) != 3 || detail.Notes == "" {
		t.Fatalf("detail = %+v", detail)
	}

	mustHelm(t, server, "rollback", helm.Target{
		Namespace: helmNamespace, Name: "web", Revision: 1, ExpectedRevision: 3, Wait: true,
	}, nil)
	if replicas := podinfoReplicas(t, "web"); replicas != "1" {
		t.Fatalf("replicas after rollback = %s", replicas)
	}
	if status := helmCLI(t, "status", "web", "-o", "json"); !strings.Contains(status, `"version":4`) {
		t.Fatalf("status = %s", status)
	}

	mustHelm(t, server, "uninstall", helm.Target{Namespace: helmNamespace, Name: "web", ExpectedRevision: 4}, nil)
	if left := kubectl(t, "", "-n", helmNamespace, "get", "deployment,service,secret", "-o", "name"); strings.TrimSpace(left) != "" {
		t.Fatalf("left behind:\n%s", left)
	}
	var releases []helm.Release
	if status := helmGet(t, server, "releases", &releases); status != http.StatusOK || len(releases) != 0 {
		t.Fatalf("status = %d, releases = %+v", status, releases)
	}
}

func TestKindHelmInstallFromRepositoryURL(t *testing.T) {
	cleanHelmNamespace(t)
	server := startKindServer(t)
	var installed helm.Result
	mustHelm(t, server, "install", helm.Change{
		Namespace: helmNamespace, Name: "repo", Chart: "podinfo", Repo: podinfoRepo, Version: podinfoOld,
		CreateNamespace: true,
	}, &installed)
	if installed.Release.ChartVersion != podinfoOld {
		t.Fatalf("release = %+v", installed.Release)
	}
}

// The app and the helm command must be able to take turns on one release.
func TestKindHelmSharesReleasesWithTheCommand(t *testing.T) {
	cleanHelmNamespace(t)
	server := startKindServer(t)
	helmCLI(t, "install", "shared", podinfoOCI, "--version", podinfoOld, "--create-namespace", "--set", "replicaCount=1")

	var releases []helm.Release
	if status := helmGet(t, server, "releases?namespace="+helmNamespace, &releases); status != http.StatusOK || len(releases) != 1 {
		t.Fatalf("status = %d, releases = %+v", status, releases)
	}

	mustHelm(t, server, "upgrade", helm.Change{
		Namespace: helmNamespace, Name: "shared", Values: "replicaCount: 2\n", ExpectedRevision: 1,
	}, nil)
	if replicas := podinfoReplicas(t, "shared"); replicas != "2" {
		t.Fatalf("replicas = %s", replicas)
	}

	helmCLI(t, "upgrade", "shared", podinfoOCI, "--version", podinfoNew, "--set", "replicaCount=3")
	if replicas := podinfoReplicas(t, "shared"); replicas != "3" {
		t.Fatalf("replicas = %s", replicas)
	}

	// This was decided before the command moved the release on to revision 3.
	status, body := helmPost(t, server, "upgrade", helm.Change{
		Namespace: helmNamespace, Name: "shared", Values: "replicaCount: 9\n", ExpectedRevision: 2,
	}, nil)
	if status != http.StatusConflict || !strings.Contains(body, "stale") {
		t.Fatalf("status = %d: %s", status, body)
	}
	if replicas := podinfoReplicas(t, "shared"); replicas != "3" {
		t.Fatalf("stale upgrade changed replicas to %s", replicas)
	}
}

func TestKindHelmFailedUpgradeShowsAndRollsBack(t *testing.T) {
	cleanHelmNamespace(t)
	server := startKindServer(t)
	mustHelm(t, server, "install", helm.Change{
		Namespace: helmNamespace, Name: "broken", Chart: podinfoOCI, Version: podinfoOld, CreateNamespace: true,
	}, nil)

	// The API server rejects this type, after Helm has started the upgrade.
	status, body := helmPost(t, server, "upgrade", helm.Change{
		Namespace: helmNamespace, Name: "broken", Values: "service:\n  type: NotAType\n", ExpectedRevision: 1,
	}, nil)
	if status == http.StatusOK {
		t.Fatal("expected the upgrade to fail")
	}
	if !strings.Contains(body, "NotAType") {
		t.Fatalf("status = %d: %s", status, body)
	}

	var detail helm.Detail
	if status := helmGet(t, server, "releases/"+helmNamespace+"/broken", &detail); status != http.StatusOK {
		t.Fatalf("detail status = %d", status)
	}
	if detail.Status != "failed" {
		t.Fatalf("detail = %+v", detail)
	}
	mustHelm(t, server, "rollback", helm.Target{
		Namespace: helmNamespace, Name: "broken", Revision: 1, ExpectedRevision: detail.Revision,
	}, nil)
	if status := helmGet(t, server, "releases/"+helmNamespace+"/broken", &detail); status != http.StatusOK || detail.Status != "deployed" {
		t.Fatalf("status = %d, detail = %+v", status, detail)
	}
}

func TestKindHelmReportsMissingChart(t *testing.T) {
	server := startKindServer(t)
	status, body := helmPost(t, server, "install", helm.Change{
		Namespace: "default", Name: "nope", Chart: "oci://ghcr.io/stefanprodan/charts/does-not-exist",
	}, nil)
	if status != http.StatusUnprocessableEntity || !strings.Contains(body, "Could not find the chart") {
		t.Fatalf("status = %d: %s", status, body)
	}
}
