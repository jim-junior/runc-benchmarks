package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/containerd/containerd"
	apievents "github.com/containerd/containerd/api/events"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
	"github.com/containerd/typeurl/v2"
)

func main() {
	const (
		address     = "/run/containerd/containerd.sock"
		namespace   = "default"
		imageRef    = "docker.io/library/alpine:latest"
		containerID = "task-create-test"
		snapshotID  = "task-create-test-snapshot"
	)

	ctx := namespaces.WithNamespace(
		context.Background(),
		namespace,
	)

	client, err := containerd.New(address)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	image, err := client.GetImage(ctx, imageRef)
	if err != nil {
		log.Fatal(err)
	}

	// Subscribe before creating the task so we do not miss the event.
	eventCh, errCh := client.Subscribe(ctx)

	container, err := client.NewContainer(
		ctx,
		containerID,
		containerd.WithImage(image),
		containerd.WithNewSnapshot(snapshotID, image),
		containerd.WithNewSpec(
			oci.WithImageConfig(image),
			oci.WithProcessArgs("sleep", "60"),
		),
	)
	if err != nil {
		log.Fatal(err)
	}

	defer container.Delete(ctx, containerd.WithSnapshotCleanup)

	// Start timing immediately before task creation.
	startedAt := time.Now()

	task, err := container.NewTask(ctx, cio.NullIO)
	if err != nil {
		log.Fatal(err)
	}

	defer task.Delete(ctx, containerd.WithProcessKill)

	// Wait until containerd publishes the matching task-create event.
	for {
		select {
		case err := <-errCh:
			if err != nil {
				log.Fatal(err)
			}

		case envelope := <-eventCh:
			if envelope == nil {
				continue
			}

			if envelope.Topic != "/tasks/create" {
				continue
			}

			event, err := typeurl.UnmarshalAny(envelope.Event)
			if err != nil {
				log.Fatal(err)
			}

			taskCreate, ok := event.(*apievents.TaskCreate)
			if !ok {
				continue
			}

			if taskCreate.ContainerID != containerID {
				continue
			}

			eventTime := envelope.Timestamp

			latency := eventTime.Sub(startedAt)

			fmt.Printf("task_create_event_latency=%s\n", latency)
			fmt.Printf("task_create_event_latency_ms=%.3f\n",
				float64(latency.Microseconds())/1000,
			)

			return

		case <-time.After(10 * time.Second):
			log.Fatal("timed out waiting for /tasks/create event")
		}
	}
}
