package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"kubehack/internal/metrics"
)

func startMetricsServer(t *testing.T) *Server {
	t.Helper()
	client := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			{Group: "metrics.k8s.io", Version: "v1beta1", Resource: "pods"}: "PodMetricsList",
		})
	server, err := New(Options{
		Token:     "secret",
		NewClient: func(string) (dynamic.Interface, error) { return client, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve()
	t.Cleanup(func() { server.Shutdown(context.Background()) })
	return server
}

func getMetrics(t *testing.T, server *Server, query string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, server.BaseURL()+"/api/v1/contexts/test/metrics?"+query, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	return response
}

func TestMetricsRoute(t *testing.T) {
	server := startMetricsServer(t)
	response := getMetrics(t, server, "kind=pods")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var snapshot metrics.Snapshot
	if err := json.NewDecoder(response.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if !snapshot.Available {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestMetricsRejectsOtherKinds(t *testing.T) {
	server := startMetricsServer(t)
	if response := getMetrics(t, server, "kind=secrets"); response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d", response.StatusCode)
	}
}
