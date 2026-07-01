# OCI task latency example

This example measures containerd task lifecycle latency for:

- task create
- task start
- task delete

It has two measurement styles:

- `--mode=events`: waits for containerd task events such as `/tasks/create`
- `--mode=client`: times the containerd Go client RPC calls directly
- `--mode=both`: prints both

Prepare an image first so image pull time is not included:

```bash
sudo ctr -n default images pull docker.io/library/alpine:latest
```

Run with the default runc Runtime v2 handler:

```bash
cd examples/oci-latency
sudo go run . --runtime io.containerd.runc.v2 --mode both
```

Run with another Runtime v2 handler:

```bash
sudo go run . --runtime io.containerd.runsc.v1 --mode both
sudo go run . --runtime io.containerd.kata.v2 --mode both
```

The event metrics represent the time from just before the client operation starts until the matching containerd task event is observed for the same container ID. The RPC metrics represent the time from just before the client call starts until that call returns.
