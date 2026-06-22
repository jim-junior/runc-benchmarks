#!/bin/bash

RUNTIMES=("runc" "kata" "gvisor")
PORTS=("8081" "8082" "8083")

echo "=================================================="
echo " Starting Application Network Performance Benchmark"
echo "=================================================="

for i in "${!RUNTIMES[@]}"; do
    RUNTIME=${RUNTIMES[$i]}
    PORT=${PORTS[$i]}
    
    echo -e "\n--------------------------------------------------"
    echo "Testing Runtime: ${RUNTIME} on Port: ${PORT}"
    echo "--------------------------------------------------"
    
    # Run wrk: 4 threads, 100 concurrent connections, for 20 seconds
    # The --latency flag forces wrk to print out detailed latency percentiles (p50, p90, p99)
    wrk -t4 -c100 -d20s --latency http://127.0.0.1:${PORT}/
    
    echo "--------------------------------------------------"
done