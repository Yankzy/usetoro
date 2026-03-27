# Toro Protocol Daemon

The Protocol Daemon initializes and manages the host node environment for the ecosystem. It connects core infrastructure dependencies and orchestrates the Agent Supervisor.

## 1. Core Responsibilities
- **Infrastructure Initialization:** Connects to PostgreSQL, NATS JetStream, and initializes shared Vector stores (Pinecone/OpenAI embeddings).
- **Dependency Containerization:** Bundles infrastructure clients into a unified dependency struct (`agents.AgentDependencies`) mapping them safely into centralized generic factories without hardcoding discrete objects.
- **Agent Lifecycle Management:** Loads the `defaults.yaml` configuration and instructs the Agent Supervisor (`supervisor.go`) to deploy the designated AI and framework routines.

## 2. Dynamic Hot Reloading

The Daemon supports zero-downtime Agent configuration reloads.

- **Trigger Mechanisms:** Administrators execute a `SIGHUP` signal to the host process or issue an HTTP `POST /reload` request directly to the Admin debugging port (e.g. `:9090`).
- **Processing Flow:** The Daemon triggers `LoadFunc()` reloading the explicit YAML string natively. It passes the updated object configurations into the Supervisor environment.
- **Diff Management:** The Supervisor identifies the configuration schema changes against actively spinning routines. It executes new agents, terminates deleted agents, and applies specific prompt/model modifications without disrupting unaffected active pipelines.
- **UI Integration:** This enables frontend UX workflows where system operators can build, customize, or disable autonomous Agents dynamically over `defaults.yaml` edits.

## 3. Directory Broadcasting

Before the Supervisor activates any Agent payload tracking, the Daemon explicitly registers the Agent's identity and inbound NATS addresses into the overarching `Almanac` database using a direct `almanac.register` network propagation, officially broadcasting its availability matrix.
