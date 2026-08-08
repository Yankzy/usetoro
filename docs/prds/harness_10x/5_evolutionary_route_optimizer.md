# Product Requirements Document (PRD)

## 5. Evolutionary Route Optimizer (Statistical Graph Self-Rewriting)

**Document Reference:** `docs/prds/harness_10x/5_evolutionary_route_optimizer.md`  
**Status:** Draft v1.0  
**Owner:** Workflow Runtime & AI Optimization  
**Subsystem:** System 4 of the 5 Core Harness Subsystems (Local Adaptation Layer)

---

# 1. Executive Summary & Core Objective

The **Evolutionary Route Optimizer** enables execution plans (DAG topologies) to rewrite their edge routing dynamically over thousands of runs based on empirical telemetry success rates.

### Core Objective
*Instead of engineers manually tweaking DAG child routing logic in YAML files, the harness statistically evaluates path completion rates (e.g. Sequence A succeeds 96% vs Sequence B succeeds 82%) and autonomously shifts default candidate routing weights.*

---

# 2. Statistical Path Evaluation Engine

The runtime tracks path completion rates in `toro_core.dag_edge_telemetry`:

```sql
CREATE TABLE IF NOT EXISTS toro_core.dag_edge_telemetry (
    dag_name VARCHAR(64) NOT NULL,
    from_node_id VARCHAR(64) NOT NULL,
    to_node_id VARCHAR(64) NOT NULL,
    total_executions BIGINT NOT NULL DEFAULT 0,
    successful_syncs BIGINT NOT NULL DEFAULT 0,
    human_interventions BIGINT NOT NULL DEFAULT 0,
    avg_entropy_reduction FLOAT8 NOT NULL DEFAULT 0.0,
    primary key (dag_name, from_node_id, to_node_id)
);
```

### Route Rewriting Algorithm
When `total_executions > 1000` and path $A$ exhibits higher Expected Information Gain than path $B$, the Evolutionary Optimizer updates the default edge weight $W(A) > W(B)$, automatically prioritizing high-yield paths.
