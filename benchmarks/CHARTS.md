# Benchmark Charts

GitHub-friendly Mermaid charts generated from the raw benchmark outputs.

## LongMemEval Recall@5

```mermaid
xychart-beta
    title "LongMemEval Recall@5"
    x-axis ["MiniLM", "bge-small", "bge-base"]
    y-axis "Recall@5" 0.96 --> 1.00
    bar [0.982, 0.990, 0.992]
```

## LongMemEval Time

```mermaid
xychart-beta
    title "LongMemEval Time (seconds)"
    x-axis ["MiniLM", "bge-small", "bge-base"]
    y-axis "Seconds" 0 --> 60
    bar [14.4, 23.0, 44.1]
```

## LoCoMo Average Recall

```mermaid
xychart-beta
    title "LoCoMo Average Recall"
    x-axis ["MiniLM", "bge-small", "bge-base"]
    y-axis "Avg Recall" 0.90 --> 0.96
    bar [0.915, 0.941, 0.949]
```

## LoCoMo Time

```mermaid
xychart-beta
    title "LoCoMo Time (seconds)"
    x-axis ["MiniLM", "bge-small", "bge-base"]
    y-axis "Seconds" 0 --> 1800
    bar [896.1, 1633.6, 1696.2]
```

## MemBench Recall@5

```mermaid
xychart-beta
    title "MemBench Recall@5"
    x-axis ["MiniLM", "bge-small", "bge-base"]
    y-axis "Recall@5" 0.75 --> 0.82
    bar [0.778, 0.804, 0.802]
```

## ConvoMem Average Recall

```mermaid
xychart-beta
    title "ConvoMem Average Recall"
    x-axis ["MiniLM", "bge-small", "bge-base"]
    y-axis "Avg Recall" 0.88 --> 0.94
    bar [0.931, 0.898, 0.920]
```

## Add Throughput

```mermaid
xychart-beta
    title "Perf Add Throughput (ops/sec)"
    x-axis ["MiniLM", "bge-small", "bge-base"]
    y-axis "Ops/sec" 0 --> 900
    bar [870.67, 888.45, 422.00]
```

## Search Throughput

```mermaid
xychart-beta
    title "Perf Search Throughput (ops/sec)"
    x-axis ["MiniLM", "bge-small", "bge-base"]
    y-axis "Ops/sec" 0 --> 1700
    bar [1616.44, 451.36, 1575.09]

## Adversarial Recall@1

```mermaid
xychart-beta
    title "Adversarial Recall@1"
    x-axis ["tagmem", "MemPalace raw"]
    y-axis "Recall@1" 0.60 --> 0.90
    bar [0.8860, 0.6600]
```

## Adversarial MRR

```mermaid
xychart-beta
    title "Adversarial MRR"
    x-axis ["tagmem", "MemPalace raw"]
    y-axis "MRR" 0.75 --> 0.95
    bar [0.9430, 0.8193]
```
```
