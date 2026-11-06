package stream

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"

	"kubehack/internal/cluster"
)

// shellScript prefers bash and falls back to sh, in one exec so that a missing
// bash does not need a second round trip or a visible error. The session
// variable is inherited by everything started in the shell, which is how
// Hangup finds those processes.
const shellScript = "export KUBEHACK_SESSION=%s; " +
	"if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"

// hangupScript signals the processes of one session. It reads /proc because a
// container may have no ps, and uses tr because not every grep has -z.
const hangupScript = `for p in /proc/[0-9]*; do
  if tr '\0' '\n' < "$p/environ" 2>/dev/null | grep -qx 'KUBEHACK_SESSION=%s'; then
    kill -HUP "${p#/proc/}" 2>/dev/null
  fi
done`

// ExecRequest selects the container to open a shell in.
type ExecRequest struct {
	Context   string
	Namespace string
	Pod       string
	Container string
	// Session is a unique ID, made only of letters and digits because it is
	// placed in a shell script.
	Session string
}

// ExecStreams connects a shell to its terminal.
type ExecStreams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Sizes  remotecommand.TerminalSizeQueue
}

// ExecRunner runs a shell until it exits or ctx is cancelled.
type ExecRunner func(ctx context.Context, request ExecRequest, streams ExecStreams) error

// ShellCloser ends the processes of a session whose connection was dropped.
type ShellCloser func(ctx context.Context, request ExecRequest) error

// RunShell is the ExecRunner for real clusters.
func RunShell(ctx context.Context, request ExecRequest, streams ExecStreams) error {
	executor, err := newExecutor(request, corev1.PodExecOptions{
		Container: request.Container,
		Command:   []string{"/bin/sh", "-c", fmt.Sprintf(shellScript, request.Session)},
		Stdin:     true,
		Stdout:    true,
		TTY:       true,
	})
	if err != nil {
		return err
	}
	return executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             streams.Stdin,
		Stdout:            streams.Stdout,
		Tty:               true,
		TerminalSizeQueue: streams.Sizes,
	})
}

// Hangup does what closing a terminal window does. Dropping an exec connection
// does not: the container runtime sends no signal, so without this the shell
// and whatever it runs stay in the container for good.
func Hangup(ctx context.Context, request ExecRequest) error {
	executor, err := newExecutor(request, corev1.PodExecOptions{
		Container: request.Container,
		Command:   []string{"/bin/sh", "-c", fmt.Sprintf(hangupScript, request.Session)},
		Stdout:    true,
	})
	if err != nil {
		return err
	}
	return executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdout: io.Discard})
}

func newExecutor(request ExecRequest, options corev1.PodExecOptions) (remotecommand.Executor, error) {
	config, err := cluster.RESTConfig(request.Context)
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(rest.AddUserAgent(config, "kubehack"))
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	url := client.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(request.Namespace).Name(request.Pod).SubResource("exec").
		VersionedParams(&options, scheme.ParameterCodec).URL()

	// Older API servers only speak SPDY, the same fallback kubectl uses.
	webSocket, err := remotecommand.NewWebSocketExecutor(config, "GET", url.String())
	if err != nil {
		return nil, fmt.Errorf("create websocket executor: %w", err)
	}
	spdy, err := remotecommand.NewSPDYExecutor(config, "POST", url)
	if err != nil {
		return nil, fmt.Errorf("create spdy executor: %w", err)
	}
	executor, err := remotecommand.NewFallbackExecutor(webSocket, spdy, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
	if err != nil {
		return nil, fmt.Errorf("create executor: %w", err)
	}
	return executor, nil
}

// SizeQueue hands terminal sizes to the executor. Only the latest size matters,
// so an unread one is replaced rather than queued behind.
type SizeQueue struct {
	sizes chan remotecommand.TerminalSize
	done  <-chan struct{}
}

// NewSizeQueue creates a queue that stops when done closes.
func NewSizeQueue(done <-chan struct{}) *SizeQueue {
	return &SizeQueue{sizes: make(chan remotecommand.TerminalSize, 1), done: done}
}

// Set records the latest size. It must only be called from one goroutine.
func (q *SizeQueue) Set(width, height uint16) {
	select {
	case <-q.sizes:
	default:
	}
	q.sizes <- remotecommand.TerminalSize{Width: width, Height: height}
}

// Next implements remotecommand.TerminalSizeQueue.
func (q *SizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case size := <-q.sizes:
		return &size
	case <-q.done:
		return nil
	}
}
