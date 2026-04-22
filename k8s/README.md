# Kubernetes Deployments

This directory contains Kubernetes manifests for deploying the Kagenti MCP OAuth system.

## Available Deployments

### envoy-demo (Production-Ready)

The **recommended** deployment using Envoy Proxy + AuthBridge with centralized Token Broker.

**Location:** `k8s/envoy-demo/`

**Architecture:**
- Envoy Proxy with AuthBridge ext_proc sidecar
- Centralized Token Broker service for OAuth management
- Production-ready, scalable design
- Namespace: `kagenti-envoy-demo`

**Documentation:** See [k8s/envoy-demo/README.md](envoy-demo/README.md)

**Key Features:**
- ✅ Envoy Proxy + AuthBridge (ext_proc)
- ✅ Centralized Token Broker
- ✅ REST API with long-polling
- ✅ Production-ready architecture
- ✅ Horizontal scalability (with Redis)

## Getting Started

```bash
# Deploy the envoy-demo
cd k8s/envoy-demo
kubectl apply -f 00-namespace.yaml
kubectl apply -f 01-token-broker.yaml
kubectl apply -f 02-mcp-server.yaml
kubectl apply -f 03-envoy-config.yaml
kubectl apply -f 04-aiagent.yaml
kubectl apply -f 05-backend.yaml
```

For detailed instructions, see the [envoy-demo README](envoy-demo/README.md).

## Architecture Overview

```
┌─────────────┐         ┌──────────────┐         ┌─────────────┐
│   Backend   │◄────────┤ Token Broker ├────────►│ MCP Server  │
│  (Phase 3)  │  Events │   Service    │Discovery│             │
└──────┬──────┘         └──────┬───────┘         └─────────────┘
       │                       │
       │ X-OAuth-Session-Key   │ Token Cache
       │                       │ Session Store
       ▼                       ▼
┌──────────────────────────────────────────────┐
│              AI Agent Pod                    │
│  ┌─────────┐  ┌───────────┐  ┌───────────┐ │
│  │  Envoy  │  │AuthBridge │  │ AI Agent  │ │
│  │ Sidecar │  │  Sidecar  │  │ Container │ │
│  │         │  │           │  │           │ │
│  │Inbound: │  │ ext_proc  │  │Listens on │ │
│  │ 15124   │◄─┤  gRPC     │  │127.0.0.1: │ │
│  │         │  │  :9090    │  │  8186     │ │
│  │Outbound:│  │           │  │           │ │
│  │ 15123   │◄─┤ Calls     │  │           │ │
│  │         │  │ Token     │  │           │ │
│  │         │  │ Broker    │  │           │ │
│  └─────────┘  └───────────┘  └───────────┘ │
└──────────────────────────────────────────────┘
```

## References

- [Envoy Demo Documentation](envoy-demo/README.md)
- [Token Broker Architecture](../docs/token_broker_architecture.md)
- [AuthBridge Documentation](../kagenti-extensions/authbridge/README.md)
- [MCP Protocol](https://modelcontextprotocol.io/)

---

**Made with Bob**