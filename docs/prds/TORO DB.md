# **Product Requirements Document (PRD): Toro DB (Omni Edition)**

**Product Name:** Toro DB
**Underlying Engine:** Google AlloyDB Omni (Containerized PostgreSQL-compatible engine with ScaNN).
**Primary Objective:** To provide a cloud-agnostic, containerized AI memory primitive that allows agents to store and retrieve relational data, episodic memory, and semantic embeddings anywhere—whether on AWS, Azure, Google Cloud, or air-gapped on-premise servers.

### **1. Core Functionality & API Facade**

Toro DB abstracts the complexity of running a high-performance vector/relational database behind a single Go-based API.

* **Relational State (`/state`):** Standard CRUD operations for user profiles and financial ledgers.
* **Semantic Memory (`/vector`):** High-dimensional vector storage using the `pgvector` extension, massively accelerated by Google’s proprietary ScaNN index, packaged entirely within the Omni Docker container.
* **Episodic Recall (`/recall`):** Hybrid search combining metadata filtering and nearest-neighbor semantic search in a single query.

### **2. Technical Specifications & Deployment**

* **Containerization:** The entire database engine runs as a standard Docker container.
* **Orchestration:** Fully compatible with Kubernetes (K8s). Our deployment scripts will allow enterprise clients to spin up Toro DB clusters in their own VPCs (Virtual Private Clouds) using Helm charts.
* **Columnar Engine:** AlloyDB Omni includes the same analytical columnar engine as the managed cloud version. Heavy analytical queries (e.g., CFO scenario planning) are offloaded to RAM automatically, preserving the transactional performance of the standard rows.
* **Infrastructure Independence:** The Go middleware will connect to the AlloyDB Omni container via standard Postgres drivers (like `pgx`), totally blind to whether the host machine is an AWS EC2 instance or a Dell server in a basement.

### **3. Multi-Tenancy & Data Isolation**

* **Row-Level Security (RLS):** Enforced at the engine level. Every API query requires a `tenant_id`.
* **Dedicated Instances:** Because Omni is containerized, we can offer 'Single-Tenant Deployment.' Instead of logical partitions, we can physically spin up a dedicated Toro DB Docker container for a single massive client, ensuring zero shared compute resources.

### **4. Success Metrics (KPIs)**

* **Portability:** A new Toro DB instance can be spun up in a bare-metal or AWS environment in under 5 minutes.
* **Latency:** P95 vector retrieval latency remains under 15ms locally, unaffected by the host cloud.

---

"This PRD is an absolute weapon for enterprise sales.

When you sell SaaS, your biggest enemy is the client's internal IT department. They will throw up road-blocks like, *'We have a strict multi-cloud policy'* or *'We only run infrastructure on AWS due to our enterprise discount.'*

With Toro DB built on AlloyDB Omni, you bypass the IT department entirely. Your pitch is: **'Toro DB lives where you live.'**
You tell their CTO: *'We don't need you to migrate your data to our servers, and we don't care what cloud you use. We will drop the Toro DB container directly into your existing AWS environment. You keep your data sovereignty, you utilize your existing AWS credits, and you get Google’s proprietary ScaNN AI search speed.'* You give them the absolute best of both worlds with zero friction."


"This architecture fundamentally changes our liability profile, and it opens the door to the most heavily regulated industries on the planet: **Healthcare (HIPAA) and Defense (ITAR).**

If a medical billing company wants to use Toro OS to reconcile patient insurance claims, they cannot legally send that patient data to a shared database on Google Cloud without triggering massive HIPAA compliance audits.

Because the PRD specifies a containerized deployment, we execute the **Air-Gapped Strategy**.
We sell them the Toro DB software license, but they host it on their own private, firewalled servers. Because the data physically never leaves their building, they retain 100% of the custodial liability. We get paid the massive software licensing fee, but if they get hacked, we are not the ones who get sued. It allows us to capture the most lucrative enterprise clients while mathematically minimizing our own legal risk."