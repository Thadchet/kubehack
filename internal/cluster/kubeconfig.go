// Package cluster reads kubeconfig and builds clients for its contexts.
package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const probeTimeout = 10 * time.Second

// ErrContextNotFound is returned for a context that is not in kubeconfig.
var ErrContextNotFound = errors.New("context not found")

// Context describes one kubeconfig context. It never carries credentials.
type Context struct {
	Name        string `json:"name"`
	Cluster     string `json:"cluster"`
	User        string `json:"user"`
	Namespace   string `json:"namespace"`
	Server      string `json:"server"`
	AuthCommand string `json:"authCommand,omitempty"`
}

// Contexts is the content of the merged kubeconfig.
type Contexts struct {
	Current  string    `json:"current"`
	Contexts []Context `json:"contexts"`
}

// Status is the result of contacting a cluster.
type Status struct {
	Reachable bool   `json:"reachable"`
	Version   string `json:"version,omitempty"`
	Error     string `json:"error,omitempty"`
}

// ListContexts reads kubeconfig on every call so that edits made outside the
// app, such as `gcloud container clusters get-credentials`, appear at once.
func ListContexts() (Contexts, error) {
	config, err := clientcmd.NewDefaultClientConfigLoadingRules().Load()
	if err != nil {
		return Contexts{}, fmt.Errorf("load kubeconfig: %w", err)
	}

	result := Contexts{Current: config.CurrentContext, Contexts: []Context{}}
	for name, entry := range config.Contexts {
		item := Context{
			Name:      name,
			Cluster:   entry.Cluster,
			User:      entry.AuthInfo,
			Namespace: entry.Namespace,
		}
		if cluster, ok := config.Clusters[entry.Cluster]; ok {
			item.Server = cluster.Server
		}
		if user, ok := config.AuthInfos[entry.AuthInfo]; ok && user.Exec != nil {
			item.AuthCommand = user.Exec.Command
		}
		result.Contexts = append(result.Contexts, item)
	}
	slices.SortFunc(result.Contexts, func(a, b Context) int {
		return strings.Compare(a.Name, b.Name)
	})
	return result, nil
}

// RESTConfig builds the client configuration for a context. The app never
// changes current-context, so it cannot affect kubectl in the user's terminals.
func RESTConfig(name string) (*rest.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	raw, err := rules.Load()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	if _, ok := raw.Contexts[name]; !ok {
		return nil, fmt.Errorf("%w: %s", ErrContextNotFound, name)
	}

	overrides := &clientcmd.ConfigOverrides{CurrentContext: name}
	config, err := clientcmd.NewNonInteractiveClientConfig(*raw, name, overrides, rules).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build client config: %w", err)
	}
	return config, nil
}

// Probe contacts the cluster with a read-only version request. A cluster that
// cannot be reached is a normal result, so only an unknown context is an error.
func Probe(ctx context.Context, name string) (Status, error) {
	config, err := RESTConfig(name)
	if errors.Is(err, ErrContextNotFound) {
		return Status{}, err
	}
	if err != nil {
		return Status{Error: err.Error()}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	// Credential plugins ignore request cancellation, and some wait for a
	// browser login, so the wait has to be bounded here.
	done := make(chan Status, 1)
	go func() { done <- fetchVersion(ctx, config) }()
	select {
	case status := <-done:
		return status, nil
	case <-ctx.Done():
		return Status{Error: fmt.Sprintf("no response within %s", probeTimeout)}, nil
	}
}

func fetchVersion(ctx context.Context, config *rest.Config) Status {
	client, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return Status{Error: err.Error()}
	}
	body, err := client.RESTClient().Get().AbsPath("/version").Do(ctx).Raw()
	if err != nil {
		return Status{Error: err.Error()}
	}

	var version struct {
		GitVersion string `json:"gitVersion"`
	}
	if err := json.Unmarshal(body, &version); err != nil {
		return Status{Error: fmt.Sprintf("parse version: %v", err)}
	}
	return Status{Reachable: true, Version: version.GitVersion}
}
