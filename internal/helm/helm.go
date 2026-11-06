// Package helm manages Helm releases with the Helm SDK. Releases stay
// compatible with the helm command, because state lives in the same Secrets.
package helm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"helm.sh/helm/v4/pkg/action"
	chartcommon "helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/loader"
	chartv2 "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/kube"
	"helm.sh/helm/v4/pkg/registry"
	ri "helm.sh/helm/v4/pkg/release"
	releasev1 "helm.sh/helm/v4/pkg/release/v1"
	"helm.sh/helm/v4/pkg/storage/driver"
	"sigs.k8s.io/yaml"
)

const (
	// operationTimeout bounds hooks, and waiting for resources when asked to.
	operationTimeout = 5 * time.Minute
	// historyLimit keeps the panel readable. Helm itself keeps more.
	historyLimit  = 20
	storageDriver = "secret"
)

// The SDK otherwise names the field manager after this program. Server-side
// apply would then treat the app and the helm command as rivals, and an upgrade
// here of a release installed there fails with a conflict on every field.
func init() {
	kube.ManagedFieldsManager = "helm"
}

var (
	// ErrNotFound means the release does not exist.
	ErrNotFound = errors.New("release not found")
	// ErrStale means the release changed after the caller last read it.
	ErrStale = errors.New("release has changed")
	// ErrInvalid means the request cannot be acted on as written.
	ErrInvalid = errors.New("invalid request")
)

// InvalidError explains a bad request in words fit to show the user. It matches
// ErrInvalid.
type InvalidError string

func (e InvalidError) Error() string {
	return string(e)
}

// Is implements errors.Is.
func (e InvalidError) Is(target error) bool {
	return target == ErrInvalid
}

// Release summarises the latest revision of a release.
type Release struct {
	Name         string    `json:"name"`
	Namespace    string    `json:"namespace"`
	Revision     int       `json:"revision"`
	Status       string    `json:"status"`
	Chart        string    `json:"chart"`
	ChartVersion string    `json:"chartVersion"`
	AppVersion   string    `json:"appVersion"`
	Updated      time.Time `json:"updated"`
}

// Revision is one entry in a release's history.
type Revision struct {
	Revision     int       `json:"revision"`
	Status       string    `json:"status"`
	Chart        string    `json:"chart"`
	ChartVersion string    `json:"chartVersion"`
	AppVersion   string    `json:"appVersion"`
	Updated      time.Time `json:"updated"`
	Description  string    `json:"description"`
}

// Detail is what the release panel shows.
type Detail struct {
	Release
	Notes   string     `json:"notes"`
	History []Revision `json:"history"`
}

// Change describes an install or upgrade.
type Change struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	// Chart is a repo/chart name, an oci:// reference or a local path. An upgrade
	// with no chart keeps the one already in the release.
	Chart string `json:"chart"`
	// Repo is a repository URL, for when Chart is a bare name.
	Repo    string `json:"repo"`
	Version string `json:"version"`
	// Values is YAML, and replaces all values the release had before.
	Values          string `json:"values"`
	CreateNamespace bool   `json:"createNamespace"`
	Wait            bool   `json:"wait"`
	DryRun          bool   `json:"dryRun"`
	// ExpectedRevision is the revision the user was looking at.
	ExpectedRevision int `json:"expectedRevision"`
}

// Result reports a change. Manifests are only set for a dry run, and leave out
// Secrets, so that a preview cannot expose them.
type Result struct {
	Release  Release `json:"release"`
	Notes    string  `json:"notes,omitempty"`
	Manifest string  `json:"manifest,omitempty"`
	Current  string  `json:"current,omitempty"`
}

// Target names an existing revision of a release.
type Target struct {
	Namespace        string `json:"namespace"`
	Name             string `json:"name"`
	Revision         int    `json:"revision"`
	ExpectedRevision int    `json:"expectedRevision"`
	Wait             bool   `json:"wait"`
}

// Client is what the API needs from Helm.
type Client interface {
	List(contextName, namespace string) ([]Release, error)
	Detail(contextName, namespace, name string) (Detail, error)
	Values(contextName, namespace, name string) (string, error)
	Install(ctx context.Context, contextName string, change Change) (Result, error)
	Upgrade(ctx context.Context, contextName string, change Change) (Result, error)
	Rollback(contextName string, target Target) error
	Uninstall(contextName string, target Target) error
}

type configurator func(contextName, namespace string) (*action.Configuration, *cli.EnvSettings, error)

// Service is the Client for real clusters.
type Service struct {
	configure configurator
}

// New creates a Service that reads the user's Helm settings, including its
// repositories and registry logins.
func New() *Service {
	return &Service{configure: configure}
}

func configure(contextName, namespace string) (*action.Configuration, *cli.EnvSettings, error) {
	settings := cli.New()
	settings.KubeContext = contextName
	settings.SetNamespace(namespace)

	config := action.NewConfiguration()
	if err := config.Init(settings.RESTClientGetter(), namespace, storageDriver); err != nil {
		return nil, nil, fmt.Errorf("connect to cluster: %w", err)
	}
	registryClient, err := registry.NewClient(
		registry.ClientOptEnableCache(true),
		registry.ClientOptWriter(io.Discard),
		registry.ClientOptCredentialsFile(settings.RegistryConfig),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create registry client: %w", err)
	}
	config.RegistryClient = registryClient
	return config, settings, nil
}

// List returns releases, from all namespaces if namespace is empty.
func (s *Service) List(contextName, namespace string) ([]Release, error) {
	config, _, err := s.configure(contextName, namespace)
	if err != nil {
		return nil, err
	}
	list := action.NewList(config)
	list.AllNamespaces = namespace == ""
	// A failed or stuck release is what the user most needs to see.
	list.All = true
	list.SetStateMask()
	found, err := list.Run()
	if err != nil {
		return nil, err
	}
	releases := make([]Release, 0, len(found))
	for _, item := range found {
		release, err := toRelease(item)
		if err != nil {
			return nil, err
		}
		releases = append(releases, summary(release))
	}
	return releases, nil
}

// Detail returns one release with its recent history, newest first.
func (s *Service) Detail(contextName, namespace, name string) (Detail, error) {
	config, _, err := s.configure(contextName, namespace)
	if err != nil {
		return Detail{}, err
	}
	found, err := action.NewHistory(config).Run(name)
	if err != nil {
		return Detail{}, describe(err)
	}
	revisions := make([]*releasev1.Release, 0, len(found))
	for _, item := range found {
		release, err := toRelease(item)
		if err != nil {
			return Detail{}, err
		}
		revisions = append(revisions, release)
	}
	if len(revisions) == 0 {
		return Detail{}, ErrNotFound
	}
	sort.Slice(revisions, func(i, j int) bool { return revisions[i].Version > revisions[j].Version })

	latest := revisions[0]
	detail := Detail{Release: summary(latest), History: []Revision{}}
	if latest.Info != nil {
		detail.Notes = latest.Info.Notes
	}
	for _, release := range revisions[:min(len(revisions), historyLimit)] {
		item := summary(release)
		description := ""
		if release.Info != nil {
			description = release.Info.Description
		}
		detail.History = append(detail.History, Revision{
			Revision:     item.Revision,
			Status:       item.Status,
			Chart:        item.Chart,
			ChartVersion: item.ChartVersion,
			AppVersion:   item.AppVersion,
			Updated:      item.Updated,
			Description:  description,
		})
	}
	return detail, nil
}

// Values returns the values the user set, as YAML. They can hold passwords, so
// the UI only asks for them to edit an upgrade.
func (s *Service) Values(contextName, namespace, name string) (string, error) {
	config, _, err := s.configure(contextName, namespace)
	if err != nil {
		return "", err
	}
	values, err := action.NewGetValues(config).Run(name)
	if err != nil {
		return "", describe(err)
	}
	if len(values) == 0 {
		return "", nil
	}
	content, err := yaml.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("encode values: %w", err)
	}
	return string(content), nil
}

// Install creates a release.
func (s *Service) Install(ctx context.Context, contextName string, change Change) (Result, error) {
	values, err := parseValues(change.Values)
	if err != nil {
		return Result{}, err
	}
	config, settings, err := s.configure(contextName, change.Namespace)
	if err != nil {
		return Result{}, err
	}

	install := action.NewInstall(config)
	install.ReleaseName = change.Name
	install.Namespace = change.Namespace
	install.CreateNamespace = change.CreateNamespace
	install.Timeout = operationTimeout
	install.WaitStrategy = waitStrategy(change.Wait)
	install.RepoURL = change.Repo
	install.Version = change.Version
	if change.DryRun {
		install.DryRunStrategy = action.DryRunServer
		install.HideSecret = true
	}

	chart, err := loadChart(&install.ChartPathOptions, change.Chart, settings)
	if err != nil {
		return Result{}, err
	}
	installed, err := install.RunWithContext(ctx, chart, values)
	if err != nil {
		return Result{}, describe(err)
	}
	return result(installed, "", change.DryRun)
}

// Upgrade changes a release. The values given replace all earlier ones, because
// the UI shows the current values for editing, so what is sent is what the user
// saw.
func (s *Service) Upgrade(ctx context.Context, contextName string, change Change) (Result, error) {
	values, err := parseValues(change.Values)
	if err != nil {
		return Result{}, err
	}
	config, settings, err := s.configure(contextName, change.Namespace)
	if err != nil {
		return Result{}, err
	}
	current, err := latest(config, change.Name, change.ExpectedRevision)
	if err != nil {
		return Result{}, err
	}

	upgrade := action.NewUpgrade(config)
	upgrade.Namespace = change.Namespace
	upgrade.Timeout = operationTimeout
	upgrade.WaitStrategy = waitStrategy(change.Wait)
	upgrade.ResetValues = true
	upgrade.RepoURL = change.Repo
	upgrade.Version = change.Version
	if change.DryRun {
		upgrade.DryRunStrategy = action.DryRunServer
		upgrade.HideSecret = true
	}

	// Without a chart these would be ignored, and the upgrade would look as if it
	// had moved to the version the user typed.
	if strings.TrimSpace(change.Chart) == "" && (strings.TrimSpace(change.Version) != "" || strings.TrimSpace(change.Repo) != "") {
		return Result{}, InvalidError("Enter the chart too. A version or repository only applies to a chart you name.")
	}

	var chart chartcommon.Charter = current.Chart
	if strings.TrimSpace(change.Chart) != "" {
		if chart, err = loadChart(&upgrade.ChartPathOptions, change.Chart, settings); err != nil {
			return Result{}, err
		}
	}
	upgraded, err := upgrade.RunWithContext(ctx, change.Name, chart, values)
	if err != nil {
		return Result{}, describe(err)
	}
	return result(upgraded, current.Manifest, change.DryRun)
}

// Rollback restores an earlier revision, as a new revision.
func (s *Service) Rollback(contextName string, target Target) error {
	config, _, err := s.configure(contextName, target.Namespace)
	if err != nil {
		return err
	}
	if _, err := latest(config, target.Name, target.ExpectedRevision); err != nil {
		return err
	}
	rollback := action.NewRollback(config)
	rollback.Version = target.Revision
	rollback.Timeout = operationTimeout
	rollback.WaitStrategy = waitStrategy(target.Wait)
	return describe(rollback.Run(target.Name))
}

// Uninstall removes a release, its resources and its history.
func (s *Service) Uninstall(contextName string, target Target) error {
	config, _, err := s.configure(contextName, target.Namespace)
	if err != nil {
		return err
	}
	if _, err := latest(config, target.Name, target.ExpectedRevision); err != nil {
		return err
	}
	uninstall := action.NewUninstall(config)
	uninstall.Timeout = operationTimeout
	uninstall.WaitStrategy = waitStrategy(false)
	uninstall.DeletionPropagation = "background"
	_, err = uninstall.Run(target.Name)
	return describe(err)
}

func waitStrategy(wait bool) kube.WaitStrategy {
	if wait {
		return kube.StatusWatcherStrategy
	}
	return kube.HookOnlyStrategy
}

func parseValues(content string) (map[string]any, error) {
	values := map[string]any{}
	if err := yaml.Unmarshal([]byte(content), &values); err != nil {
		return nil, InvalidError("Values aren't valid YAML. " + err.Error())
	}
	if values == nil {
		values = map[string]any{}
	}
	return values, nil
}

func loadChart(options *action.ChartPathOptions, reference string, settings *cli.EnvSettings) (chartcommon.Charter, error) {
	path, err := options.LocateChart(strings.TrimSpace(reference), settings)
	if err != nil {
		return nil, InvalidError("Could not find the chart. " + err.Error())
	}
	chart, err := loader.Load(path)
	if err != nil {
		return nil, InvalidError("Could not read the chart. " + err.Error())
	}
	accessor, err := chartcommon.NewAccessor(chart)
	if err != nil {
		return nil, fmt.Errorf("read chart: %w", err)
	}
	if err := action.CheckDependencies(chart, accessor.MetaDependencies()); err != nil {
		return nil, InvalidError("The chart is missing dependencies. " + err.Error())
	}
	return chart, nil
}

// latest returns the current revision, and refuses to go on if it isn't the one
// the user decided from. Without this, a change made by someone else in between
// would be silently overwritten or rolled over.
func latest(config *action.Configuration, name string, expected int) (*releasev1.Release, error) {
	found, err := config.Releases.Last(name)
	if err != nil {
		return nil, describe(err)
	}
	release, err := toRelease(found)
	if err != nil {
		return nil, err
	}
	if release.Version != expected {
		return nil, fmt.Errorf("%w: now at revision %d", ErrStale, release.Version)
	}
	return release, nil
}

func describe(err error) error {
	if errors.Is(err, driver.ErrReleaseNotFound) {
		return ErrNotFound
	}
	return err
}

func toRelease(item ri.Releaser) (*releasev1.Release, error) {
	switch release := item.(type) {
	case *releasev1.Release:
		return release, nil
	case releasev1.Release:
		return &release, nil
	}
	return nil, fmt.Errorf("unsupported release type %T", item)
}

func summary(release *releasev1.Release) Release {
	item := Release{Name: release.Name, Namespace: release.Namespace, Revision: release.Version}
	if release.Info != nil {
		item.Status = release.Info.Status.String()
		item.Updated = release.Info.LastDeployed
	}
	if metadata := chartMetadata(release.Chart); metadata != nil {
		item.Chart = metadata.Name
		item.ChartVersion = metadata.Version
		item.AppVersion = metadata.AppVersion
	}
	return item
}

func chartMetadata(chart *chartv2.Chart) *chartv2.Metadata {
	if chart == nil {
		return nil
	}
	return chart.Metadata
}

func result(item ri.Releaser, current string, dryRun bool) (Result, error) {
	release, err := toRelease(item)
	if err != nil {
		return Result{}, err
	}
	outcome := Result{Release: summary(release)}
	if release.Info != nil {
		outcome.Notes = release.Info.Notes
	}
	if dryRun {
		outcome.Manifest = withoutSecrets(fullManifest(release))
		outcome.Current = withoutSecrets(current)
	}
	return outcome, nil
}

// fullManifest adds hooks to the preview, because they run in the cluster too.
func fullManifest(release *releasev1.Release) string {
	var content strings.Builder
	content.WriteString(release.Manifest)
	for _, hook := range release.Hooks {
		content.WriteString("\n---\n# Hook: " + hook.Path + "\n" + hook.Manifest)
	}
	return content.String()
}

var (
	documentSeparator = regexp.MustCompile(`(?m)^---\s*$`)
	secretKind        = regexp.MustCompile(`(?m)^kind:\s*["']?Secret["']?\s*$`)
)

// withoutSecrets drops Secret documents. Everything else in a manifest is meant
// to be read, but these hold what the rest of the app keeps hidden.
func withoutSecrets(manifest string) string {
	if manifest == "" {
		return ""
	}
	documents := documentSeparator.Split(manifest, -1)
	kept := make([]string, 0, len(documents))
	for _, document := range documents {
		if strings.TrimSpace(document) == "" {
			continue
		}
		if secretKind.MatchString(document) {
			kept = append(kept, "\n# A Secret is hidden here.\n")
			continue
		}
		kept = append(kept, document)
	}
	return "---" + strings.Join(kept, "---")
}
