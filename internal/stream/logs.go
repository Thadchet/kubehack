// Package stream connects long-running pod sessions to the UI.
package stream

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"kubehack/internal/cluster"
)

// maxLineBytes bounds memory for one line. A longer line arrives in pieces.
const maxLineBytes = 64 * 1024

// LogRequest selects one container's log.
type LogRequest struct {
	Context   string
	Namespace string
	Pod       string
	Container string
	Previous  bool
	Follow    bool
	TailLines int64
}

// LogOpener starts reading a log. The stream ends when ctx is cancelled.
type LogOpener func(ctx context.Context, request LogRequest) (io.ReadCloser, error)

// OpenLogs is the LogOpener for real clusters.
func OpenLogs(ctx context.Context, request LogRequest) (io.ReadCloser, error) {
	config, err := cluster.RESTConfig(request.Context)
	if err != nil {
		return nil, err
	}
	client, err := kubernetes.NewForConfig(rest.AddUserAgent(config, "kubehack"))
	if err != nil {
		return nil, fmt.Errorf("create client: %w", err)
	}

	options := &corev1.PodLogOptions{
		Container: request.Container,
		Previous:  request.Previous,
		Follow:    request.Follow,
		TailLines: &request.TailLines,
	}
	return client.CoreV1().Pods(request.Namespace).GetLogs(request.Pod, options).Stream(ctx)
}

// ScanLines calls emit for each line until the reader ends or emit returns
// false. A final line without a newline is still delivered.
func ScanLines(reader io.Reader, emit func(string) bool) error {
	buffered := bufio.NewReaderSize(reader, maxLineBytes)
	for {
		chunk, err := buffered.ReadSlice('\n')
		if len(chunk) > 0 {
			// Logs can hold arbitrary bytes, and the UI receives JSON strings.
			line := strings.ToValidUTF8(strings.TrimRight(string(chunk), "\r\n"), "�")
			if !emit(line) {
				return nil
			}
		}
		switch {
		case err == nil, errors.Is(err, bufio.ErrBufferFull):
		case errors.Is(err, io.EOF):
			return nil
		default:
			return err
		}
	}
}
