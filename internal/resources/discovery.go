// Package resources discovers and watches any Kubernetes resource, including
// custom resources, without code specific to a kind.
package resources

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
)

const discoveryTimeout = 15 * time.Second

// APIResource is a resource type that can be listed and watched.
type APIResource struct {
	Group      string `json:"group"`
	Version    string `json:"version"`
	Resource   string `json:"resource"`
	Kind       string `json:"kind"`
	Namespaced bool   `json:"namespaced"`
}

// Discovery is the result of asking a cluster what it serves.
type Discovery struct {
	Resources []APIResource `json:"resources"`
	// Warnings names API groups that failed, such as a broken aggregated API.
	// The rest of the result is still usable.
	Warnings []string `json:"warnings,omitempty"`
}

// Discover lists the watchable resource types of a cluster.
func Discover(ctx context.Context, config *rest.Config) (Discovery, error) {
	config = rest.CopyConfig(config)
	config.Timeout = discoveryTimeout
	client, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return Discovery{}, fmt.Errorf("create discovery client: %w", err)
	}

	result := Discovery{Resources: []APIResource{}}
	lists, err := discovery.ServerPreferredResourcesWithContext(ctx, discovery.ToDiscoveryInterfaceWithContext(client))
	if err != nil {
		if !discovery.IsGroupDiscoveryFailedError(err) {
			return Discovery{}, fmt.Errorf("discover resources: %w", err)
		}
		result.Warnings = append(result.Warnings, err.Error())
	}

	for _, list := range lists {
		groupVersion, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			result.Warnings = append(result.Warnings, err.Error())
			continue
		}
		for _, resource := range list.APIResources {
			if strings.Contains(resource.Name, "/") ||
				!slices.Contains(resource.Verbs, "list") ||
				!slices.Contains(resource.Verbs, "watch") {
				continue
			}
			result.Resources = append(result.Resources, APIResource{
				Group:      groupVersion.Group,
				Version:    groupVersion.Version,
				Resource:   resource.Name,
				Kind:       resource.Kind,
				Namespaced: resource.Namespaced,
			})
		}
	}
	slices.SortFunc(result.Resources, func(a, b APIResource) int {
		return cmp.Or(cmp.Compare(a.Group, b.Group), cmp.Compare(a.Resource, b.Resource))
	})
	return result, nil
}
