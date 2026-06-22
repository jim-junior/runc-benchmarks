# Runtime Benchmarking

Extensive benchmark of container runtimes `runc`, `urunc`, `kata containers` and `runsc`.

## Metrics matrix

| Area        | Metrics                                                                      | Tooling (candidate)                          |
| ----------- | ---------------------------------------------------------------------------- | -------------------------------------------- |
| Lifecycle   | OCI create/start/delete latency, app/HTTP readiness                          | TODO                                         |
| Memory      | Guest-assigned vs VMM vs host overhead (not RSS-only)                        | cgroups, smem/procfs, per-runtime accounting |
| CPU         | Sandbox overhead vs native                                                   | stress-ng / app-level workloads              |
| Storage     | Throughput + IOPS (reads/writes)                                             | fio                                          |
| Network     | Throughput, latency, RTT, per-hypervisor (QEMU/KVM, Firecracker) virtio path | fortio                                       |
| Tail/stress | p99 behavior, constrained guest/host resources, spikes                       | constrained cgroup runs                      |


### Network

Main metrics to be collected are:

- Throughput
- Baseline RTT
- and Tail Latency

Measured using `fortio`

```sh
sudo nerdctl run --runtime runc -d --name net-runc -p 8081:80 nginx:alpine

docker run --rm --net=host -v $(pwd):/var/fortio fortio/fortio load -qps 2000 -c 50 -t 30s -json /var/fortio/runc_report.json http://127.0.0.1:8081/
```

Outputs in `./fortio/runc_report.json`

Also explored `wrk`, `wrk2` and `iperf3`.

- Why not use these?

  - `iperf3` and `wrk2` face the Coordinated Omission problem. This means the tail latencies(p99, p99.9), which are KEY, are skewed, making a struggling system look incredibly fast on paper

### Storage


Benchmarked using `fio`

```sh
sudo nerdctl run --rm --runtime=runc \
  -v "$(pwd)/io-benchmarks:/bench" \
  openeuler/fio fio --name=test-runc --directory=/bench --rw=randrw --bs=4k --size=512M --direct=1 --time_based --runtime=30 --group_reporting --output-format=json > ./io-benchmarks/fio_runc.json
```

Outputs in `io-benchmarks/fio_runc.json`
