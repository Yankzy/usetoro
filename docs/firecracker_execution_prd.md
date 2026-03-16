# PRD: Toro Trusted Execution Environment (TEE) via Firecracker

## 1. Objective

To build a high-performance, deterministic, and secure "Financial Cloud" that allows 3rd-party developers to deploy AI agents (TAP - Toro Agent Protocol) directly onto Toro infrastructure. By using Firecracker microVMs, we ensure that accounting logic and insurance risk-modeling execute in a "Verified Environment" that is isolated from the core Toro Go backend.

---

## 2. Technical Architecture: The "Hosted" Model

### 2.1 MicroVM Orchestration

* **Engine:** Firecracker VMM (Rust-based).
* **Orchestrator:** A dedicated Go service (`toro-visor`) running on the host.
* **Isolation:** Each 3rd-party agent runs in its own microVM with restricted CPU (e.g., 1 vCPU) and RAM (e.g., 128MB - 512MB).
* **RootFS:** Minimalist Linux kernel + a read-only root file system containing the agent binary.

### 2.2 Host-Guest Communication (NATS over VSOCK)

* **The Bridge:** We will not use standard networking (TAP/TUN) to avoid overhead and security leaks. Instead, we use `AF_VSOCK`.
* **Protocol:** A NATS-proxy on the host maps VSOCK ports to specific NATS JetStream subjects.
* **Workflow:**
1. Host receives a transaction event via NATS.
2. Host pushes payload to VSOCK.
3. Agent inside Firecracker processes data and signs the result.
4. Agent pushes signed "Proof of Execution" back through VSOCK to the Toro Ledger.



---

## 3. Operational Requirements

### 3.1 Determinism & Snapshotting

* **State Persistence:** Use Firecracker’s "Snapshot" feature to freeze an agent's RAM state after a successful reconciliation cycle.
* **Recovery:** If an agent crashes or the host reboots, we resume from the exact micro-second of the last snapshot. This is critical for 3-year SLA guarantees.

### 3.2 Security & Compliance (Institutional Grade)

* **Code Signing:** 3rd-party code must be cryptographically signed. The `toro-visor` will refuse to spawn a VM if the signature doesn't match the developer’s public key stored in the Toro Vault.
* **Resource Quotas:** Hard-limit execution time (e.g., max 2 seconds per reconciliation task) to prevent "infinite loop" attacks or crypto-mining.
* **Data Masking:** The host "Gate" service strips PII (Personally Identifiable Information) before passing data to the 3rd-party VM, unless the user has explicitly authorized full access.

---

## 4. The Developer Workflow (Developer Experience)

1. **Build:** Developer writes an agent in Go or Rust using the `toro-sdk`.
2. **Push:** Developer runs `toro-cli deploy`.
3. **Validate:** Toro CI/CD compiles the code into a static binary, scans for malicious syscalls, and packages it into an `.ext4` rootfs.
4. **Spawn:** Toro-visor spawns a warm-booted Firecracker VM in <150ms.
5. **Monitor:** Developer sees logs via the Fignode Pro Desktop app (piped from the VM via NATS).

---

## 5. Performance Benchmarks (The "Casablanca" Standards)

* **Boot Time:** < 200ms from request to "Ready" state.
* **Density:** 500+ concurrent agents per physical bare-metal server in Morocco.
* **Overhead:** < 5% CPU overhead compared to native execution.
* **Latency:** Round-trip VSOCK communication < 1ms.

---

## 6. Next Steps for Engineering (Casablanca Team)

1. **PoC:** Implement a basic Go wrapper that spawns a Firecracker VM and sends a "Hello World" via VSOCK.
2. **NATS Integration:** Build the VSOCK-to-NATS proxy to allow the VM to "see" the JetStream.
3. **Snapshot Test:** Verify that we can "Pause" a running reconciliation task and "Resume" it on a different host without data loss.
