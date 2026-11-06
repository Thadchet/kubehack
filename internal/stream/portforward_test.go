package stream

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

// fakeForwards holds a run open until the test ends it.
type fakeForwards struct {
	mu         sync.Mutex
	resolveErr error
	runErr     error
	checkErr   error
	drop       chan error
	runs       int
	ports      []int
}

func newFakeForwards() *fakeForwards {
	return &fakeForwards{drop: make(chan error, 1)}
}

func (f *fakeForwards) Resolve(context.Context, ForwardRequest) (ForwardTarget, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return ForwardTarget{Pod: "p1", UID: "u1", Port: 80}, f.resolveErr
}

func (f *fakeForwards) Run(ctx context.Context, request ForwardRequest, _ ForwardTarget, ready func(int)) error {
	f.mu.Lock()
	f.runs++
	f.ports = append(f.ports, request.LocalPort)
	err := f.runErr
	f.mu.Unlock()
	if err != nil {
		return err
	}
	port := request.LocalPort
	if port == 0 {
		port = 34567
	}
	ready(port)
	select {
	case err := <-f.drop:
		return err
	case <-ctx.Done():
		return nil
	}
}

func (f *fakeForwards) Check(context.Context, ForwardRequest, ForwardTarget) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checkErr
}

func (f *fakeForwards) setCheck(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checkErr = err
}

func testManager(t *testing.T, driver ForwardDriver) *ForwardManager {
	t.Helper()
	manager := NewForwardManager(driver)
	manager.checkInterval = 5 * time.Millisecond
	t.Cleanup(func() { manager.Close(context.Background()) })
	return manager
}

var testRequest = ForwardRequest{Context: "c", Namespace: "n", Kind: ForwardPod, Name: "p1", Port: 80}

func waitForState(t *testing.T, manager *ForwardManager, id string, want ForwardState) Forward {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		forward, err := manager.Get(id)
		if err == nil && forward.State == want {
			return forward
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("forward never became %s", want)
	return Forward{}
}

func TestStartReportsChosenPort(t *testing.T) {
	manager := testManager(t, newFakeForwards())
	forward, err := manager.Start(testRequest)
	if err != nil {
		t.Fatal(err)
	}
	if forward.State != ForwardActive || forward.LocalPort != 34567 || forward.Pod != "p1" {
		t.Fatalf("forward = %+v", forward)
	}
}

func TestFailedStartIsNotKept(t *testing.T) {
	for name, setup := range map[string]func(*fakeForwards){
		"resolve": func(f *fakeForwards) { f.resolveErr = ErrForwardTarget },
		"run":     func(f *fakeForwards) { f.runErr = ErrLocalPortInUse },
	} {
		t.Run(name, func(t *testing.T) {
			driver := newFakeForwards()
			setup(driver)
			manager := testManager(t, driver)
			if _, err := manager.Start(testRequest); err == nil {
				t.Fatal("expected error")
			}
			if forwards := manager.List(); len(forwards) != 0 {
				t.Fatalf("forwards = %+v", forwards)
			}
		})
	}
}

func TestDroppedConnectionIsReported(t *testing.T) {
	driver := newFakeForwards()
	manager := testManager(t, driver)
	changes, unsubscribe := manager.Subscribe()
	defer unsubscribe()
	forward, err := manager.Start(testRequest)
	if err != nil {
		t.Fatal(err)
	}
	<-changes

	driver.drop <- errors.New("lost connection to pod")
	lost := waitForState(t, manager, forward.ID, ForwardLost)
	if lost.Message == "" {
		t.Fatal("lost forward has no reason")
	}
	select {
	case <-changes:
	case <-time.After(time.Second):
		t.Fatal("subscriber was not told")
	}
}

func TestDroppedConnectionNamesDeletedPod(t *testing.T) {
	driver := newFakeForwards()
	manager := testManager(t, driver)
	manager.checkInterval = time.Hour
	forward, err := manager.Start(testRequest)
	if err != nil {
		t.Fatal(err)
	}
	driver.setCheck(PodGoneError{Reason: "Pod p1 was deleted."})
	driver.drop <- errors.New("lost connection to pod")
	if lost := waitForState(t, manager, forward.ID, ForwardLost); lost.Message != "Pod p1 was deleted." {
		t.Fatalf("message = %q", lost.Message)
	}
}

// An idle forward gets no error when its pod goes, so only the check can see it.
func TestIdleForwardNoticesPodGone(t *testing.T) {
	driver := newFakeForwards()
	manager := testManager(t, driver)
	forward, err := manager.Start(testRequest)
	if err != nil {
		t.Fatal(err)
	}
	driver.setCheck(PodGoneError{Reason: "Pod p1 was deleted."})
	if lost := waitForState(t, manager, forward.ID, ForwardLost); lost.Message != "Pod p1 was deleted." {
		t.Fatalf("message = %q", lost.Message)
	}
}

func TestBriefClusterOutageIsTolerated(t *testing.T) {
	driver := newFakeForwards()
	manager := testManager(t, driver)
	manager.checkInterval = 20 * time.Millisecond
	forward, err := manager.Start(testRequest)
	if err != nil {
		t.Fatal(err)
	}
	driver.setCheck(errors.New("timeout"))
	time.Sleep(30 * time.Millisecond)
	driver.setCheck(nil)
	time.Sleep(100 * time.Millisecond)
	if current, _ := manager.Get(forward.ID); current.State != ForwardActive {
		t.Fatalf("state = %s", current.State)
	}

	driver.setCheck(errors.New("timeout"))
	waitForState(t, manager, forward.ID, ForwardLost)
}

func TestRestartKeepsLocalPort(t *testing.T) {
	driver := newFakeForwards()
	manager := testManager(t, driver)
	forward, err := manager.Start(testRequest)
	if err != nil {
		t.Fatal(err)
	}
	driver.drop <- errors.New("lost")
	waitForState(t, manager, forward.ID, ForwardLost)

	restarted, err := manager.Restart(forward.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.State != ForwardActive || restarted.Message != "" || restarted.LocalPort != forward.LocalPort {
		t.Fatalf("restarted = %+v", restarted)
	}
	if driver.ports[1] != forward.LocalPort {
		t.Fatalf("second run asked for port %d", driver.ports[1])
	}
}

func TestFailedRestartStaysListed(t *testing.T) {
	driver := newFakeForwards()
	manager := testManager(t, driver)
	forward, err := manager.Start(testRequest)
	if err != nil {
		t.Fatal(err)
	}
	driver.drop <- errors.New("lost")
	waitForState(t, manager, forward.ID, ForwardLost)

	driver.mu.Lock()
	driver.runErr = ErrLocalPortInUse
	driver.mu.Unlock()
	if _, err := manager.Restart(forward.ID); !errors.Is(err, ErrLocalPortInUse) {
		t.Fatalf("err = %v", err)
	}
	if current, _ := manager.Get(forward.ID); current.State != ForwardLost || current.Message == "" {
		t.Fatalf("forward = %+v", current)
	}
}

func TestRestartRefusesRunningForward(t *testing.T) {
	manager := testManager(t, newFakeForwards())
	forward, err := manager.Start(testRequest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Restart(forward.ID); !errors.Is(err, ErrForwardBusy) {
		t.Fatalf("err = %v", err)
	}
}

func TestStopRemovesWithoutReportingLoss(t *testing.T) {
	driver := newFakeForwards()
	manager := testManager(t, driver)
	forward, err := manager.Start(testRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Stop(forward.ID); err != nil {
		t.Fatal(err)
	}
	if forwards := manager.List(); len(forwards) != 0 {
		t.Fatalf("forwards = %+v", forwards)
	}
	if err := manager.Stop(forward.ID); !errors.Is(err, ErrForwardNotFound) {
		t.Fatalf("err = %v", err)
	}
}

func TestStopRemovesLostForward(t *testing.T) {
	driver := newFakeForwards()
	manager := testManager(t, driver)
	forward, err := manager.Start(testRequest)
	if err != nil {
		t.Fatal(err)
	}
	driver.drop <- errors.New("lost")
	waitForState(t, manager, forward.ID, ForwardLost)
	if err := manager.Stop(forward.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCloseEndsForwardsAndRefusesNewOnes(t *testing.T) {
	driver := newFakeForwards()
	manager := testManager(t, driver)
	if _, err := manager.Start(testRequest); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	manager.Close(ctx)
	if ctx.Err() != nil {
		t.Fatal("close did not finish")
	}
	if _, err := manager.Start(testRequest); !errors.Is(err, ErrManagerClosed) {
		t.Fatalf("err = %v", err)
	}
}

func readyPod(name, uid string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "n", UID: types.UID(uid), Labels: map[string]string{"app": "web"}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}},
		}}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
		},
	}
}

func testService(selector map[string]string, target intstr.IntOrString) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "n"},
		Spec: corev1.ServiceSpec{
			Selector: selector,
			Ports:    []corev1.ServicePort{{Port: 80, TargetPort: target}},
		},
	}
}

var serviceRequest = ForwardRequest{Namespace: "n", Kind: ForwardService, Name: "web", Port: 80}

func TestResolveServiceTranslatesPorts(t *testing.T) {
	selector := map[string]string{"app": "web"}
	for name, test := range map[string]struct {
		target intstr.IntOrString
		want   int
	}{
		"number":  {intstr.FromInt32(9000), 9000},
		"name":    {intstr.FromString("http"), 8080},
		"omitted": {intstr.IntOrString{}, 80},
	} {
		t.Run(name, func(t *testing.T) {
			client := fake.NewClientset(testService(selector, test.target), readyPod("p1", "u1"))
			target, err := resolveForward(context.Background(), client, serviceRequest)
			if err != nil {
				t.Fatal(err)
			}
			if target.Pod != "p1" || target.Port != test.want {
				t.Fatalf("target = %+v", target)
			}
		})
	}
}

func TestResolveServiceSkipsUnreadyPods(t *testing.T) {
	unready := readyPod("a", "u0")
	unready.Status.Conditions[0].Status = corev1.ConditionFalse
	client := fake.NewClientset(
		testService(map[string]string{"app": "web"}, intstr.FromInt32(80)),
		unready,
		readyPod("b", "u1"),
	)
	target, err := resolveForward(context.Background(), client, serviceRequest)
	if err != nil {
		t.Fatal(err)
	}
	if target.Pod != "b" {
		t.Fatalf("pod = %s", target.Pod)
	}
}

func TestResolveRejects(t *testing.T) {
	selector := map[string]string{"app": "web"}
	pending := readyPod("p1", "u1")
	pending.Status.Phase = corev1.PodPending

	for name, test := range map[string]struct {
		request ForwardRequest
		client  *fake.Clientset
	}{
		"service without selector": {serviceRequest, fake.NewClientset(testService(nil, intstr.FromInt32(80)))},
		"unknown service port": {
			ForwardRequest{Namespace: "n", Kind: ForwardService, Name: "web", Port: 81},
			fake.NewClientset(testService(selector, intstr.FromInt32(80)), readyPod("p1", "u1")),
		},
		"no ready pods":      {serviceRequest, fake.NewClientset(testService(selector, intstr.FromInt32(80)))},
		"unknown named port": {serviceRequest, fake.NewClientset(testService(selector, intstr.FromString("nope")), readyPod("p1", "u1"))},
		"missing pod":        {ForwardRequest{Namespace: "n", Kind: ForwardPod, Name: "p1", Port: 80}, fake.NewClientset()},
		"missing service":    {serviceRequest, fake.NewClientset()},
		"pod not running": {
			ForwardRequest{Namespace: "n", Kind: ForwardPod, Name: "p1", Port: 80},
			fake.NewClientset(pending),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := resolveForward(context.Background(), test.client, test.request); !errors.Is(err, ErrForwardTarget) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestCheckForward(t *testing.T) {
	request := ForwardRequest{Namespace: "n"}
	target := ForwardTarget{Pod: "p1", UID: "u1"}
	stopped := readyPod("p1", "u1")
	stopped.Status.Phase = corev1.PodFailed

	for name, test := range map[string]struct {
		client *fake.Clientset
		gone   bool
	}{
		"running":  {fake.NewClientset(readyPod("p1", "u1")), false},
		"deleted":  {fake.NewClientset(), true},
		"replaced": {fake.NewClientset(readyPod("p1", "u2")), true},
		"stopped":  {fake.NewClientset(stopped), true},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkForward(context.Background(), test.client, request, target)
			var gone PodGoneError
			if errors.As(err, &gone) != test.gone {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestForwardAddressIsLoopback(t *testing.T) {
	if ip := net.ParseIP(forwardAddress); ip == nil || !ip.IsLoopback() {
		t.Fatalf("forwards listen on %s", forwardAddress)
	}
}
