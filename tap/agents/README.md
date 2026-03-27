# Toro Agent Plugin Registry (`tap/agents`)

The `agents` package serves as the centralized Plugin Connector for all localized AI Agents within the Toro ecosystem. It strictly isolates explicit business logic routines from the core networking framework.

## 1. Core Responsibilities
- **Dependency Inversion:** Receives the global `agent.AgentDependencies` struct from the Daemon, containing infrastructure clients (`DBPool`, `WalletManager`, and `EntityResolver`).
- **Constructor Mapping:** Maps the target `internal_module` strings (e.g., `cleanup-agent`) defined in the `defaults.yaml` boot configuration to their corresponding Go struct constructors.
- **Micro-transaction Binding:** Wraps outbound agent `EventBus` parameters using `micrion.NewTolledEventBus`. This structurally enforces that agents pay native network inference tolls per JetStream message.

## 2. Adding a New Agent
To deploy a new Agent into the Toro ecosystem, developers do not modify the `ProtocolDaemon`. The lifecycle framework remains untouched.

1. Build a new Go package under `tap/agents/` (e.g., `tap/agents/fraud_detector`).
2. Open `tap/agents/registry.go`.
3. Import your new package block natively.
4. Insert a new `supervisor.RegisterInternalAgent` initialization block inside the `RegisterAll` payload.
5. If your agent executes system state mutations over JetStream or Postgres, ensure its EventBus/DB instance is wrapped with the `micrion` wrappers inside the closure to fund its execution compute.

## 3. UI Template Hot Reloading
The `RegisterAll` function dictates exactly which backend engines the Supervisor has available. 
When operations generate a new Agent via the frontend UI, the UI modifies the `defaults.yaml` configuration array dynamically. The Supervisor catches this configuration update, queries the `registry.go` mapping for the explicit `internal_module` template, and hot-boots the specific engine seamlessly.
