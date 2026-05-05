# Daemon CLI ONNX Timing Note

Measured on the supported Docker/ONNX path after the daemon-backed CLI timeout fix:

- direct `add`: `797 ms`
- direct `search`: `778 ms`
- daemon-backed cold `add`: `4860 ms`
- daemon-backed warm `search`: `11 ms`

Conclusion: the daemon-backed path pays the embedded ONNX initialization cost on the first cold request, but then strongly amortizes later CLI latency by reusing the hot provider and repository state.
