package helm

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"helm.sh/helm/v4/pkg/action"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/cli"
	kubefake "helm.sh/helm/v4/pkg/kube/fake"
	"helm.sh/helm/v4/pkg/registry"
	"helm.sh/helm/v4/pkg/storage"
	"helm.sh/helm/v4/pkg/storage/driver"
)

// writeChart makes a small chart on disk, with a Secret and a hook.
func writeChart(t *testing.T, version string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "demo")
	files := map[string]string{
		"Chart.yaml":  "apiVersion: v2\nname: demo\nversion: " + version + "\nappVersion: \"1.0\"\n",
		"values.yaml": "greeting: hello\nreplicas: 1\n",
		"templates/config.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}\n" +
			"data:\n  greeting: {{ .Values.greeting }}\n",
		"templates/secret.yaml": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: {{ .Release.Name }}\n" +
			"stringData:\n  password: top-secret-value\n",
		"templates/hook.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}-hook\n" +
			"  annotations:\n    helm.sh/hook: pre-install\n",
		"templates/NOTES.txt": "Thanks for installing {{ .Release.Name }}.\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// testService shares one in-memory store between actions, like the cluster does.
func testService(t *testing.T) *Service {
	t.Helper()
	store := storage.Init(driver.NewMemory())
	registryClient, err := registry.NewClient(registry.ClientOptWriter(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	return &Service{configure: func(string, string) (*action.Configuration, *cli.EnvSettings, error) {
		config := action.NewConfiguration()
		config.Releases = store
		config.KubeClient = &kubefake.PrintingKubeClient{Out: io.Discard, LogOutput: io.Discard}
		config.Capabilities = common.DefaultCapabilities
		config.RegistryClient = registryClient
		return config, cli.New(), nil
	}}
}

func install(t *testing.T, service *Service, chart string, values string) Result {
	t.Helper()
	result, err := service.Install(context.Background(), "c", Change{
		Namespace: "default", Name: "demo", Chart: chart, Values: values,
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestInstallThenList(t *testing.T) {
	service := testService(t)
	result := install(t, service, writeChart(t, "1.0.0"), "")
	if result.Release.Revision != 1 || result.Release.Status != "deployed" {
		t.Fatalf("release = %+v", result.Release)
	}
	if result.Manifest != "" {
		t.Fatal("a real install should not return a preview")
	}
	releases, err := service.List("c", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].Chart != "demo" || releases[0].ChartVersion != "1.0.0" || releases[0].AppVersion != "1.0" {
		t.Fatalf("releases = %+v", releases)
	}
}

func TestDryRunInstallChangesNothingAndHidesSecrets(t *testing.T) {
	service := testService(t)
	result, err := service.Install(context.Background(), "c", Change{
		Namespace: "default", Name: "demo", Chart: writeChart(t, "1.0.0"), DryRun: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Manifest, "greeting: hello") {
		t.Fatalf("manifest = %s", result.Manifest)
	}
	if !strings.Contains(result.Manifest, "# Hook: ") {
		t.Fatalf("hook missing from manifest: %s", result.Manifest)
	}
	if strings.Contains(result.Manifest, "top-secret-value") {
		t.Fatalf("secret in preview: %s", result.Manifest)
	}
	if releases, _ := service.List("c", ""); len(releases) != 0 {
		t.Fatalf("releases = %+v", releases)
	}
}

func TestInvalidValuesAreRejected(t *testing.T) {
	service := testService(t)
	_, err := service.Install(context.Background(), "c", Change{
		Namespace: "default", Name: "demo", Chart: writeChart(t, "1.0.0"), Values: "a: [",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingChartIsExplained(t *testing.T) {
	service := testService(t)
	_, err := service.Install(context.Background(), "c", Change{
		Namespace: "default", Name: "demo", Chart: "/no/such/chart",
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestUpgradeWithoutChartKeepsItAndReplacesValues(t *testing.T) {
	service := testService(t)
	install(t, service, writeChart(t, "1.0.0"), "greeting: first\nreplicas: 3\n")

	result, err := service.Upgrade(context.Background(), "c", Change{
		Namespace: "default", Name: "demo", Values: "greeting: second\n", ExpectedRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Release.Revision != 2 || result.Release.ChartVersion != "1.0.0" {
		t.Fatalf("release = %+v", result.Release)
	}
	values, err := service.Values("c", "default", "demo")
	if err != nil {
		t.Fatal(err)
	}
	// replicas was removed in the editor, so it must not survive.
	if values != "greeting: second\n" {
		t.Fatalf("values = %q", values)
	}
}

func TestVersionWithoutChartIsRefused(t *testing.T) {
	service := testService(t)
	install(t, service, writeChart(t, "1.0.0"), "")
	for name, change := range map[string]Change{
		"version": {Namespace: "default", Name: "demo", Version: "2.0.0", ExpectedRevision: 1},
		"repo":    {Namespace: "default", Name: "demo", Repo: "https://example.test", ExpectedRevision: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.Upgrade(context.Background(), "c", change); !errors.Is(err, ErrInvalid) {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if detail, _ := service.Detail("c", "default", "demo"); detail.Revision != 1 {
		t.Fatalf("revision = %d", detail.Revision)
	}
}

func TestUpgradeToNewChart(t *testing.T) {
	service := testService(t)
	install(t, service, writeChart(t, "1.0.0"), "")
	result, err := service.Upgrade(context.Background(), "c", Change{
		Namespace: "default", Name: "demo", Chart: writeChart(t, "2.0.0"), ExpectedRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Release.ChartVersion != "2.0.0" {
		t.Fatalf("release = %+v", result.Release)
	}
}

func TestDryRunUpgradeShowsBothSidesWithoutChanging(t *testing.T) {
	service := testService(t)
	install(t, service, writeChart(t, "1.0.0"), "")
	result, err := service.Upgrade(context.Background(), "c", Change{
		Namespace: "default", Name: "demo", Values: "greeting: changed\n", DryRun: true, ExpectedRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Current, "greeting: hello") || !strings.Contains(result.Manifest, "greeting: changed") {
		t.Fatalf("current = %s\nmanifest = %s", result.Current, result.Manifest)
	}
	if strings.Contains(result.Current+result.Manifest, "top-secret-value") {
		t.Fatal("secret in preview")
	}
	detail, err := service.Detail("c", "default", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Revision != 1 || len(detail.History) != 1 {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestStaleRevisionIsRefused(t *testing.T) {
	service := testService(t)
	install(t, service, writeChart(t, "1.0.0"), "")
	if _, err := service.Upgrade(context.Background(), "c", Change{
		Namespace: "default", Name: "demo", ExpectedRevision: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// All of these were decided while looking at revision 1.
	_, upgradeErr := service.Upgrade(context.Background(), "c", Change{
		Namespace: "default", Name: "demo", ExpectedRevision: 1,
	})
	rollbackErr := service.Rollback("c", Target{Namespace: "default", Name: "demo", Revision: 1, ExpectedRevision: 1})
	uninstallErr := service.Uninstall("c", Target{Namespace: "default", Name: "demo", ExpectedRevision: 1})
	for name, err := range map[string]error{"upgrade": upgradeErr, "rollback": rollbackErr, "uninstall": uninstallErr} {
		if !errors.Is(err, ErrStale) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if detail, _ := service.Detail("c", "default", "demo"); detail.Revision != 2 {
		t.Fatalf("revision = %d", detail.Revision)
	}
}

func TestRollbackAddsRevisionAndHistoryIsNewestFirst(t *testing.T) {
	service := testService(t)
	install(t, service, writeChart(t, "1.0.0"), "greeting: first\n")
	if _, err := service.Upgrade(context.Background(), "c", Change{
		Namespace: "default", Name: "demo", Values: "greeting: second\n", ExpectedRevision: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.Rollback("c", Target{Namespace: "default", Name: "demo", Revision: 1, ExpectedRevision: 2}); err != nil {
		t.Fatal(err)
	}
	detail, err := service.Detail("c", "default", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if detail.Revision != 3 || len(detail.History) != 3 || detail.History[0].Revision != 3 || detail.History[2].Revision != 1 {
		t.Fatalf("detail = %+v", detail)
	}
	if !strings.Contains(detail.Notes, "Thanks for installing") {
		t.Fatalf("notes = %q", detail.Notes)
	}
	if values, _ := service.Values("c", "default", "demo"); values != "greeting: first\n" {
		t.Fatalf("values = %q", values)
	}
}

func TestUninstallRemovesRelease(t *testing.T) {
	service := testService(t)
	install(t, service, writeChart(t, "1.0.0"), "")
	if err := service.Uninstall("c", Target{Namespace: "default", Name: "demo", ExpectedRevision: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Detail("c", "default", "demo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestMissingReleaseIsNotFound(t *testing.T) {
	service := testService(t)
	if _, err := service.Upgrade(context.Background(), "c", Change{
		Namespace: "default", Name: "nope", ExpectedRevision: 1,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestWithoutSecrets(t *testing.T) {
	manifest := "---\n# Source: a\nkind: ConfigMap\nmetadata:\n  name: a\n---\nkind: Secret\nstringData:\n  p: hidden\n" +
		"---\nkind: \"Secret\"\ndata:\n  p: aGlkZGVu\n---\nkind: Deployment\nspec:\n  template:\n    kind: Secret\n"
	got := withoutSecrets(manifest)
	if strings.Contains(got, "hidden\n") || strings.Contains(got, "aGlkZGVu") {
		t.Fatalf("got = %s", got)
	}
	if !strings.Contains(got, "kind: ConfigMap") || !strings.Contains(got, "kind: Deployment") {
		t.Fatalf("got = %s", got)
	}
}
