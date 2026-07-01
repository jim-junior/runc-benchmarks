package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	apievents "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/typeurl/v2"
)

const (
	taskCreateTopic = "/tasks/create"
	taskStartTopic  = "/tasks/start"
	taskDeleteTopic = "/tasks/delete"
)

type eventWaiter struct {
	containerID string
	events      map[string]chan time.Time
}

func newEventWaiter(containerID string) *eventWaiter {
	return &eventWaiter{
		containerID: containerID,
		events: map[string]chan time.Time{
			taskCreateTopic: make(chan time.Time, 1),
			taskStartTopic:  make(chan time.Time, 1),
			taskDeleteTopic: make(chan time.Time, 1),
		},
	}
}

func startEventWaiter(ctx context.Context, client *containerd.Client, containerID string) (*eventWaiter, error) {
	waiter := newEventWaiter(containerID)
	eventCh, errCh := client.Subscribe(ctx)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case err := <-errCh:
				if err != nil && ctx.Err() == nil {
					log.Printf("event subscription error: %v", err)
				}
			case envelope := <-eventCh:
				if envelope == nil {
					continue
				}
				if _, ok := waiter.events[envelope.Topic]; !ok {
					continue
				}

				event, err := typeurl.UnmarshalAny(envelope.Event)
				if err != nil {
					log.Printf("decode event %s: %v", envelope.Topic, err)
					continue
				}
				if !eventMatchesContainer(event, waiter.containerID) {
					continue
				}

				observedAt := envelope.Timestamp
				if observedAt.IsZero() {
					observedAt = time.Now()
				}

				select {
				case waiter.events[envelope.Topic] <- observedAt:
				default:
				}
			}
		}
	}()

	return waiter, nil
}

func (w *eventWaiter) wait(ctx context.Context, topic string) (time.Time, error) {
	ch, ok := w.events[topic]
	if !ok {
		return time.Time{}, fmt.Errorf("unsupported topic %q", topic)
	}

	select {
	case at := <-ch:
		return at, nil
	case <-ctx.Done():
		return time.Time{}, ctx.Err()
	}
}

func eventMatchesContainer(event any, containerID string) bool {
	switch e := event.(type) {
	case *apievents.TaskCreate:
		return e.ContainerID == containerID
	case *apievents.TaskStart:
		return e.ContainerID == containerID
	case *apievents.TaskDelete:
		return e.ContainerID == containerID
	default:
		return false
	}
}

func main() {
	var (
		address   = flag.String("address", "/run/containerd/containerd.sock", "containerd socket")
		namespace = flag.String("namespace", "default", "containerd namespace")
		runtime   = flag.String("runtime", "io.containerd.runc.v2", "containerd runtime name")
		imageRef  = flag.String("image", "docker.io/library/alpine:latest", "image already present in containerd")
		id        = flag.String("id", fmt.Sprintf("oci-latency-%d", time.Now().UnixNano()), "container ID to create")
		command   = flag.String("cmd", "sleep 60", "command executed through /bin/sh -c")
		mode      = flag.String("mode", "events", "events, client, or both")
		timeout   = flag.Duration("timeout", 20*time.Second, "timeout for each event wait")
	)
	flag.Parse()

	if *mode != "events" && *mode != "client" && *mode != "both" {
		log.Fatalf("invalid --mode %q; expected events, client, or both", *mode)
	}

	ctx := namespaces.WithNamespace(context.Background(), *namespace)

	client, err := containerd.New(*address)
	if err != nil {
		log.Fatalf("connect to containerd: %v", err)
	}
	defer client.Close()

	image, err := client.GetImage(ctx, *imageRef)
	if err != nil {
		log.Fatalf("get image %q: %v\nPull it first, for example: sudo ctr -n %s images pull %s", *imageRef, err, *namespace, *imageRef)
	}

	var waiter *eventWaiter
	var eventCtx context.Context
	var stopEvents context.CancelFunc
	if *mode == "events" || *mode == "both" {
		eventCtx, stopEvents = context.WithCancel(ctx)
		defer stopEvents()
		waiter, err = startEventWaiter(eventCtx, client, *id)
		if err != nil {
			log.Fatalf("subscribe to containerd events: %v", err)
		}
	}

	snapshotID := *id + "-snapshot"
	container, err := client.NewContainer(
		ctx,
		*id,
		containerd.WithImage(image),
		containerd.WithNewSnapshot(snapshotID, image),
		containerd.WithRuntime(*runtime, nil),
		containerd.WithNewSpec(
			oci.WithImageConfig(image),
			oci.WithProcessArgs("/bin/sh", "-c", *command),
		),
	)
	if err != nil {
		log.Fatalf("create container metadata: %v", err)
	}
	defer func() {
		if err := container.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
			log.Printf("cleanup container: %v", err)
		}
	}()

	createStarted := time.Now()
	task, err := container.NewTask(ctx, cio.NullIO)
	createReturned := time.Now()
	if err != nil {
		log.Fatalf("task create failed: %v", err)
	}

	if *mode == "events" || *mode == "both" {
		printEventLatency(ctx, waiter, taskCreateTopic, createStarted, *timeout)
	}
	if *mode == "client" || *mode == "both" {
		printClientLatency("task_create_rpc", createStarted, createReturned)
	}

	waitCh, err := task.Wait(ctx)
	if err != nil {
		log.Fatalf("task wait setup failed: %v", err)
	}

	startStarted := time.Now()
	if err := task.Start(ctx); err != nil {
		log.Fatalf("task start failed: %v", err)
	}
	startReturned := time.Now()

	if *mode == "events" || *mode == "both" {
		printEventLatency(ctx, waiter, taskStartTopic, startStarted, *timeout)
	}
	if *mode == "client" || *mode == "both" {
		printClientLatency("task_start_rpc", startStarted, startReturned)
	}

	if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
		log.Printf("task kill: %v", err)
	}
	select {
	case status := <-waitCh:
		_, _, _ = status.Result()
	case <-time.After(*timeout):
		log.Fatalf("timed out waiting for task exit")
	}

	deleteStarted := time.Now()
	_, err = task.Delete(ctx)
	deleteReturned := time.Now()
	if err != nil {
		log.Fatalf("task delete failed: %v", err)
	}

	if *mode == "events" || *mode == "both" {
		printEventLatency(ctx, waiter, taskDeleteTopic, deleteStarted, *timeout)
	}
	if *mode == "client" || *mode == "both" {
		printClientLatency("task_delete_rpc", deleteStarted, deleteReturned)
	}
}

func printEventLatency(ctx context.Context, waiter *eventWaiter, topic string, started time.Time, timeout time.Duration) {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	observedAt, err := waiter.wait(waitCtx, topic)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s_event_error=%v\n", metricName(topic), err)
		return
	}

	printDuration(metricName(topic)+"_event", observedAt.Sub(started))
}

func printClientLatency(name string, started, returned time.Time) {
	printDuration(name, returned.Sub(started))
}

func printDuration(name string, d time.Duration) {
	fmt.Printf("%s_ns=%d\n", name, d.Nanoseconds())
	fmt.Printf("%s_ms=%.3f\n", name, float64(d.Microseconds())/1000.0)
}

func metricName(topic string) string {
	return strings.Trim(strings.ReplaceAll(topic, "/", "_"), "_")
}
