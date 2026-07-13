package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
)

const (
	containerdSocket = "/run/containerd/containerd.sock"
	namespace        = "default"

	imageRef = "docker.io/jimjuniorb/fio:0.1"
	runtime  = "io.containerd.runc.v2"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ctx = namespaces.WithNamespace(ctx, namespace)

	client, err := containerd.New(containerdSocket)
	if err != nil {
		log.Fatalf("connect to containerd: %v", err)
	}
	defer client.Close()

	err = runFIO(
		ctx,
		client,
		"./fio-runc.json",
		"./fio-runc.stderr.log",
	)
	if err != nil {
		log.Fatalf("fio benchmark failed: %v", err)
	}

	log.Println("fio output written to ./fio-runc.json")
}

func runFIO(
	ctx context.Context,
	client *containerd.Client,
	outputPath string,
	stderrPath string,
) error {
	runID := fmt.Sprintf("fio-runc-%d", time.Now().UnixNano())
	snapshotID := runID + "-snapshot"

	image, err := getOrPullImage(ctx, client, imageRef)
	if err != nil {
		return fmt.Errorf("prepare image: %w", err)
	}

	/*
		This becomes the process argv inside the container:

		fio --name=test-runc \
		    --directory=/bench \
		    --rw=randrw \
		    --bs=4k \
		    --size=512M \
		    --direct=1 \
		    --time_based \
		    --runtime=30 \
		    --group_reporting \
		    --output-format=json
	*/
	processArgs := []string{
		"fio",
		"--name=test-runc",
		"--directory=/bench",
		"--rw=randrw",
		"--bs=4k",
		"--size=512M",
		"--direct=1",
		"--time_based",
		"--runtime=30",
		"--group_reporting",
		"--output-format=json",
	}

	container, err := client.NewContainer(
		ctx,
		runID,
		containerd.WithImage(image),
		containerd.WithNewSnapshot(snapshotID, image),
		containerd.WithRuntime(runtime, nil),
		containerd.WithNewSpec(
			oci.WithImageConfig(image),
			oci.WithProcessArgs(processArgs...),
		),
	)
	if err != nil {
		return fmt.Errorf("create container: %w", err)
	}

	// Equivalent to nerdctl run --rm: remove the container and snapshot.
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(
			namespaces.WithNamespace(context.Background(), namespace),
			15*time.Second,
		)
		defer cancel()

		if err := container.Delete(
			cleanupCtx,
			containerd.WithSnapshotCleanup,
		); err != nil {
			log.Printf("warning: delete container %q: %v", runID, err)
		}
	}()

	stdoutFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create stdout file: %w", err)
	}
	defer stdoutFile.Close()

	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return fmt.Errorf("create stderr file: %w", err)
	}
	defer stderrFile.Close()

	task, err := container.NewTask(
		ctx,
		cio.NewCreator(
			cio.WithStreams(nil, stdoutFile, stderrFile),
		),
	)
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	defer func() {
		cleanupCtx, cancel := context.WithTimeout(
			namespaces.WithNamespace(context.Background(), namespace),
			10*time.Second,
		)
		defer cancel()

		if _, err := task.Delete(cleanupCtx); err != nil &&
			!errdefs.IsNotFound(err) {
			log.Printf("warning: delete task %q: %v", runID, err)
		}
	}()

	// Register the wait before Start. This avoids missing the exit of a
	// short-lived process.
	exitStatusCh, err := task.Wait(ctx)
	if err != nil {
		return fmt.Errorf("wait for task: %w", err)
	}

	if err := task.Start(ctx); err != nil {
		return fmt.Errorf("start task: %w", err)
	}

	select {
	case status := <-exitStatusCh:
		exitCode, exitedAt, err := status.Result()
		if err != nil {
			return fmt.Errorf("read task exit status: %w", err)
		}

		if err := stdoutFile.Sync(); err != nil {
			return fmt.Errorf("flush stdout file: %w", err)
		}

		if err := stderrFile.Sync(); err != nil {
			return fmt.Errorf("flush stderr file: %w", err)
		}

		if exitCode != 0 {
			return fmt.Errorf(
				"fio exited with status %d at %s; inspect %s",
				exitCode,
				exitedAt.Format(time.RFC3339Nano),
				stderrPath,
			)
		}

		return nil

	case <-ctx.Done():
		// Best-effort termination if the benchmark times out.
		_ = task.Kill(
			namespaces.WithNamespace(context.Background(), namespace),
			9, // SIGKILL
		)

		return fmt.Errorf("fio execution timed out: %w", ctx.Err())
	}
}

func getOrPullImage(
	ctx context.Context,
	client *containerd.Client,
	ref string,
) (containerd.Image, error) {
	image, err := client.GetImage(ctx, ref)
	if err == nil {
		// Ensure an already-present image is unpacked.
		if err := image.Unpack(ctx, containerd.DefaultSnapshotter); err != nil &&
			!errdefs.IsAlreadyExists(err) {
			return nil, fmt.Errorf("unpack existing image: %w", err)
		}

		return image, nil
	}

	if !errdefs.IsNotFound(err) {
		return nil, fmt.Errorf("get image: %w", err)
	}

	image, err = client.Pull(
		ctx,
		ref,
		containerd.WithPullUnpack,
	)
	if err != nil {
		return nil, fmt.Errorf("pull image: %w", err)
	}

	return image, nil
}
