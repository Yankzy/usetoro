# Toro Protocol Daemon

The Protocol Daemon initializes and manages the host node environment for the ecosystem. It connects core infrastructure dependencies and orchestrates the Agent Supervisor.

## 1. Core Responsibilities
- **Infrastructure Initialization:** Connects to PostgreSQL, NATS JetStream, and initializes shared Vector stores (Pinecone/OpenAI embeddings).
- **Dependency Containerization:** Bundles infrastructure clients into a unified dependency struct (`agents.AgentDependencies`) mapping them safely into centralized generic factories without hardcoding discrete objects.
- **Agent Lifecycle Management:** Loads the `defaults.yml` configuration and instructs the Agent Supervisor (`supervisor.go`) to deploy the designated AI and framework routines.

## 2. Dynamic Hot Reloading

The Daemon supports zero-downtime Agent configuration reloads.

- **Trigger Mechanisms:** Administrators execute a `SIGHUP` signal to the host process or issue an HTTP `POST /reload` request directly to the Admin debugging port (e.g. `:9090`).
- **Processing Flow:** The Daemon triggers `LoadFunc()` reloading the explicit YAML string. It passes the updated object configurations into the Supervisor environment.
- **Diff Management:** The Supervisor identifies the configuration schema changes against actively spinning routines. It executes new agents, terminates deleted agents, and applies specific prompt/model modifications without disrupting unaffected active pipelines.
- **UI Integration:** This enables frontend UX workflows where system operators can build, customize, or disable autonomous Agents dynamically over `defaults.yml` edits.

## 3. Directory Broadcasting

Before the Supervisor activates any Agent payload tracking, the Daemon explicitly registers the Agent's identity and inbound NATS addresses into the overarching `Almanac` database using a direct `almanac.register` network propagation, officially broadcasting its availability matrix.

## 4. Usage

### Go Application Initialization

To embed the Protocol Daemon into your host application, instantiate it and execute `Run(ctx)`. You must provide a `LoadFunc` which the Daemon calls to yield the latest configuration state during startups and hot reloads.

```go
package main

import (
	"context"
	"log/slog"
	
	"github.com/Yankzy/usetoro/internal/config"
	"github.com/Yankzy/usetoro/tap/pkg/daemon"
)

func main() {
	logger := slog.Default()
	
	// Define how the latest configuration is loaded
	loadFunc := func() (*config.Config, error) {
		return config.Load("configs/defaults.yml")
	}

	// Initialize the Daemon. 
	// The admin port defaults to ":9090" if omitted.
	d := daemon.New(logger, loadFunc, ":9090")

	// Run blocks until the context is canceled or a fatal error occurs
	if err := d.Run(context.Background()); err != nil {
		logger.Error("Daemon exited with error", "err", err)
	}
}
```

### Terminal execution and Operator Commands

Once the daemon is actively running, System Administrators and DevOps tooling can interact with it directly from the terminal.

**1. Checking Health:**
Verify the daemon is alive (used for K8s readiness/liveness probes):
```bash
curl http://localhost:9090/health
# Returns: OK
```

**2. Hot Reloading via Signal (`SIGHUP`):**
Send a hangup signal to the process identifier:
```bash
# Find the PID of the daemon process
PID=$(lsof -t -i:9090)

# Send SIGHUP to trigger zero-downtime configuration reload
kill -SIGHUP $PID
```

**3. Hot Reloading via HTTP API:**
Alternatively, trigger a reload over the network:
```bash
curl -X POST http://localhost:9090/reload
# Returns: Agents Reloaded
```

**4. Debugging and Profiling:**
A standard `pprof` server is attached to track performance bottlenecks or goroutine leaks:
```bash
go tool pprof http://localhost:9090/debug/pprof/profile?seconds=30
```

### Frontend Client Execution

For UI frameworks or control plane dashboards, administrators can wire interactive toggles directly to the Daemon's network interface to hot-swap AI payloads or change environments without severing existing WebSocket connections.

Here is an example using `fetch` via Javascript/Typescript:

```typescript
/**
 * Triggers the Toro Daemon to diff the configuration against 
 * active supervisors and dynamically recycle changed Agents.
 */
async function triggerAgentHotReload(): Promise<void> {
  try {
    // The Admin Server is hosted on the specified admin port (e.g. 9090)
    const response = await fetch('http://localhost:9090/reload', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      }
    });

    if (!response.ok) {
      const errMsg = await response.text();
      throw new Error(`Daemon responded with status ${response.status}: ${errMsg}`);
    }

    console.log("✅ Toro Protocol Daemon successfully reloaded Agents.");
  } catch (error) {
    console.error("❌ Failed to contact daemon. Is it running?", error);
  }
}
```
