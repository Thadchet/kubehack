package api

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/client-go/dynamic"

	"kubehack/internal/settings"
)

func TestReadOnlyBlocksEveryWriteRoute(t *testing.T) {
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetReadOnly("prod", true); err != nil {
		t.Fatal(err)
	}
	clientBuilt := false
	server, err := New(Options{
		Token:    "secret",
		Settings: store,
		NewClient: func(string) (dynamic.Interface, error) {
			clientBuilt = true
			return nil, io.EOF
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	t.Cleanup(func() { server.http.Close() })

	post := func(path, body string) (int, string) {
		t.Helper()
		request, err := http.NewRequest(http.MethodPost, server.BaseURL()+"/api/v1/contexts/prod/"+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer secret")
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		content, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, string(content)
	}

	target := `{"version":"v1","resource":"pods","namespace":"default","name":"p1","uid":"u"}`
	for _, path := range []string{"delete", "scale", "restart", "apply"} {
		status, body := post(path, target)
		if status != http.StatusForbidden || !strings.Contains(body, `"readOnly"`) {
			t.Errorf("%s: %d %s", path, status, body)
		}
	}
	if clientBuilt {
		t.Error("a client was built for a read-only context")
	}

	// A dry run writes nothing, so previews stay available.
	status, _ := post("apply", `{"version":"v1","resource":"pods","name":"p1","dryRun":true}`)
	if status == http.StatusForbidden {
		t.Error("dry run was blocked")
	}
	if !clientBuilt {
		t.Error("dry run never reached the cluster client")
	}
}
