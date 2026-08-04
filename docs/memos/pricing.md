# Unified Pricing and Credit System

**Status:** Proposed
**Audience:** Engineering, Product, Platform, Finance
**Purpose:** Define a unified pricing architecture for the Agent Execution Platform that minimizes operational complexity, provides transparent billing, and creates a sustainable free tier while preserving a frictionless developer experience.

---

# Executive Summary

The platform will adopt a **single, unified credit system** for all users. Every execution on the platform will consume prepaid credits that are purchased directly through our platform.

The platform will own and manage all AI provider accounts and API keys. Users will never be required to supply their own OpenAI, Anthropic, Google, or other model provider credentials.

This architecture provides a consistent execution path for every workflow, significantly reduces engineering complexity, simplifies support, enables centralized optimization, and provides complete visibility into usage and costs.

The pricing model separates:

* Variable AI consumption.
* Platform resource consumption.
* Subscription features.

Each component can evolve independently while maintaining a simple experience for customers.

---

# Core Principles

The pricing system will be designed around the following principles.

## 1. One Billing System

Every customer purchases prepaid credits from the platform.

Credits are the only mechanism used to pay for AI execution.

Users never purchase tokens directly from model providers while using the platform.

The platform becomes the single billing relationship.

---

## 2. Platform Managed AI Providers

The platform owns all API keys for supported model providers.

Examples include:

* OpenAI
* Anthropic
* Google
* xAI
* Mistral
* Future providers

When a workflow selects a model, the execution engine automatically routes requests using the platform-managed credentials.

This eliminates:

* User API key management
* Provider-specific authentication
* Multiple execution paths
* Complex permission models

Every execution follows the same infrastructure pipeline.

---

## 3. Unified Execution Pipeline

Every workflow executes through the identical pipeline regardless of model.

Workflow

* Agent execution
* DAG scheduling
* Memory retrieval
* Tool execution
* LLM routing
* Response generation
* Cost accounting
* Audit logging
* Webhook delivery

Engineering only maintains one production-grade execution flow.

---

# Credit System

Credits represent prepaid purchasing power.

Every AI request deducts credits based on actual provider costs.

Each execution records:

* Model used
* Prompt tokens
* Completion tokens
* Total tokens
* Credit cost
* Execution duration
* Workflow ID
* Agent ID

Users can inspect the exact cost of every node inside every workflow.

---

# Transparent Cost Reporting

Every workflow should expose:

## Workflow Summary

* Total credits consumed
* Total tokens
* Number of nodes executed
* Total execution time

## Node Breakdown

Every node displays:

* Model
* Prompt tokens
* Completion tokens
* Credits consumed
* Retry count
* Latency

This provides complete transparency and helps developers optimize workflows.

---

# Free Tier

The free tier exists for learning, experimentation, and prototype development.

It is not intended for production workloads.

Free accounts receive:

* Platform account
* Workflow editor
* Agent builder
* SDK access
* Webhook support
* Basic logging
* Community support

Platform limits:

* Maximum 250 workflow executions per month
* Maximum 10 active workflows
* Maximum 3 concurrent executions
* Maximum execution duration: 5 minutes
* 7-day log retention
* Standard queue priority
* Basic observability
* Limited storage for workflow artifacts
* No team collaboration
* No custom domains
* No guaranteed availability

Credits must still be purchased for AI usage.

The monthly execution quota covers platform infrastructure only.

---

# Pro Tier

Designed for professional developers and small businesses.

Additional capabilities include:

* Higher execution quotas
* Increased concurrency
* Faster execution queues
* Longer workflow duration
* Extended log retention
* Advanced monitoring
* Scheduled workflows
* Team collaboration
* Environment management
* Version history
* Priority support

Credits continue to pay for AI usage.

The subscription primarily increases platform capabilities.

---

# Enterprise Tier

Enterprise customers receive:

* Dedicated infrastructure
* High availability
* SLA
* Advanced security
* Compliance features
* Private networking
* Single Sign-On
* Audit exports
* Dedicated support
* Custom limits
* Volume pricing

---

# Resource Quotas

Platform resources consume real infrastructure.

Every account will have quotas for:

* Workflow executions
* Concurrent executions
* CPU time
* Memory allocation
* Storage
* Webhook deliveries
* Queue priority
* Scheduled jobs
* Event history
* Log retention

These quotas protect platform stability while ensuring predictable operating costs.

---

# Credit Consumption

Credits pay only for variable consumption.

Examples include:

* LLM inference
* Embedding generation
* Image generation
* Audio transcription
* Audio synthesis
* Future AI services

Infrastructure quotas remain independent from credit balances.

---

# Developer Experience

Developers should never need to:

* Manage AI provider accounts
* Rotate API keys
* Handle provider authentication
* Configure provider SDKs
* Implement billing logic

The SDK should expose a single interface.

Example:

Choose model.

Execute workflow.

Receive result.

The platform manages everything else.

---

# Platform Benefits

This architecture provides significant advantages.

Engineering

* One execution pipeline
* Lower maintenance cost
* Faster feature development
* Simpler testing
* Fewer edge cases

Operations

* Centralized monitoring
* Centralized billing
* Unified analytics
* Easier fraud detection
* Easier rate limiting

Customer Experience

* No API key management
* Simple onboarding
* Transparent costs
* Predictable pricing
* Single account for every provider

Business

* Direct customer relationship
* Unified billing
* Better customer retention
* Easier introduction of new providers
* Flexible pricing evolution without SDK changes

---

# Long-Term Vision

The pricing architecture should remain provider-agnostic.

As new AI providers become available, they can be integrated behind the unified execution layer without requiring changes from developers.

The platform becomes the execution infrastructure for autonomous agents rather than a thin wrapper around individual model APIs.

Developers interact with one SDK, one billing system, one execution engine, and one operational model, regardless of the underlying AI providers powering each workflow.
