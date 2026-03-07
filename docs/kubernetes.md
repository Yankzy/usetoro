# Kubernetes Transition & Codebase Audit Report

## 1. Executive Summary
The goal of this transition is to move the UseToro platform from a Docker Compose-based microservice architecture to a robust, highly-available Kubernetes environment. Furthermore, to avoid explicitly managing each of the 14+ services, we will adopt the **Operator Pattern** by introducing a Custom Resource Definition (CRD) that declaratively manages the entire platform deployment.

## 2. Codebase Audit (Current State)
The current architecture is composed of a mix of Go services, Python workers, and stateful infrastructure services defined centrally in `container/docker-compose.yml`.

### 2.1 Services Inventory
1. **API & Edge Services**:
   - `nginx`: Reverse proxy routing to `gate` and `graphql`.
   - `gate`: Go API Gateway handling REST requests.
   - `graphql`: Go GraphQL API service.
   - `ws`: Go WebSocket service for real-time communication.

2. **Core Domain Services**:
   - `fignode`: Core product domain service (Go).
   - `protocol`: Protocol or AI handler (Go, requires OpenAI keys).
   - `sync`: QBO (QuickBooks Online) sync service (Go).

3. **Background & Async Workers**:
   - `cdc-worker`: Go-based Change Data Capture worker.
   - `python-worker`: Python-based worker for asynchronous tasks (likely Celery).
   - `migrator`: Goose migration container to run schema updates on boot.

4. **Infrastructure & Stateful Services**:
   - `db`: TimescaleDB (PostgreSQL) loaded with custom configurations (`wal_level=logical`).
   - `redis`: Redis cache/queue.
   - `nats-1, nats-2, nats-3`: A 3-node NATS JetStream cluster for pub/sub messaging.

### 2.2 Network & Routing
- The services use a custom bridge network (`toro-net`).
- NGINX routes traffic to internal ports (8080) on `gate`, `graphql`, and WebSocket paths.

## 3. Recommended Kubernetes Architecture

Transitioning 14 interconnected services manually to raw manifests (`Deployment`, `Service`, `StatefulSet`, `ConfigMap`) causes immense configuration drift overhead. We will introduce a **Kubernetes Operator** to automate this.

### 3.1 The Custom Resource Definition (CRD)
We will define a custom resource named `ToroPlatform` (e.g., `toro.io/v1alpha1`). The operator will watch for instances of this resource and automatically spin up or update the underlying Kubernetes primitives.

**Example `ToroPlatform` Spec:**
```yaml
apiVersion: toro.io/v1alpha1
kind: ToroPlatform
metadata:
  name: toro-production
spec:
  version: "v1.2.0"
  global:
    environment: "prod"
    databaseUrlSecret: "db-credentials"
  components:
    apiGateway:
      replicas: 3
    graphql:
      replicas: 3
    fignode:
      replicas: 2
    sync:
      replicas: 1
    cdcWorker:
      replicas: 1
    pythonWorker:
      replicas: 2
  infrastructure:
    nats:
      clusterSize: 3 # Automatically sets up NATS StatefulSet
```

### 3.2 Resource Mappings
When the `ToroPlatform` CR is created, the Operator will generate:
- **Deployments**: `gate`, `graphql`, `ws`, `fignode`, `protocol`, `sync`, `cdc-worker`, `python-worker`.
- **StatefulSets**: `db` (TimescaleDB), `redis`, `nats` (3 replicas with anti-affinity).
- **Jobs**: `migrator` (Runs as a pre-sync or init hook Helm-style Job before deploying updated APIs).
- **Services & Ingress**: `nginx` will be replaced entirely by a Kubernetes **Ingress Controller** (e.g., NGINX Ingress or Gateway API) routing directly to `gate` and `graphql` ClusterIP Services.

## 4. Operator Restructure Plan

To produce the CRD and the controller, we will restructure the project by introducing a new `kubernetes/operator` (or `operator`) directory at the root, scaffolded using **Kubebuilder**.

### Implementation Steps
1. **Initialize Kubebuilder**: Scaffold the Go operator project.
2. **Define the API**: Create the `ToroPlatform` type struct representing the YAML shown above.
3. **Develop the Reconciler**: Write the reconciliation loop in Go to create/update Deployments and Services based on the observed state of the CR.
4. **Makefile Updates**: Tie the Docker build pipeline into the operator's image generation (e.g., `make docker-build docker-push deploy`).
5. **Helm Packaging (Optional)**: Package the Operator itself as a Helm chart for easy bootstrapping into any cluster.
