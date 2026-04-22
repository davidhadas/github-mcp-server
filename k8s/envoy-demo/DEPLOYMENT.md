# Complete Deployment Guide

This guide provides step-by-step instructions for deploying the complete Envoy-based Kagenti MCP OAuth system.

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [GitHub OAuth App Setup](#github-oauth-app-setup)
3. [Build Docker Images](#build-docker-images)
4. [Deploy to Kubernetes](#deploy-to-kubernetes)
5. [Verify Deployment](#verify-deployment)
6. [Access the System](#access-the-system)
7. [Troubleshooting](#troubleshooting)

## Prerequisites

### Required Tools

- **Kubernetes cluster**: kind, minikube, or cloud provider
- **kubectl**: v1.24+
- **Docker**: v20.10+
- **Go**: v1.21+ (for building from source)
- **Git**: For cloning the repository

### Kubernetes Cluster Setup

#### Option 1: kind (Recommended for local testing)

```bash
# Install kind
go install sigs.k8s.io/kind@latest

# Create cluster with port mappings
cat <<EOF | kind create cluster --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 30184  # MCP Server
    hostPort: 30184
  - containerPort: 30186  # AI Agent
    hostPort: 30186
  - containerPort: 30187  # Backend
    hostPort: 30187
EOF
```

#### Option 2: minikube

```bash
# Install minikube
brew install minikube  # macOS
# or download from https://minikube.sigs.k8s.io/

# Start cluster
minikube start --cpus=4 --memory=8192
```

#### Option 3: Cloud Provider

Use your cloud provider's Kubernetes service (GKE, EKS, AKS, etc.)

## GitHub OAuth App Setup

1. **Create GitHub OAuth App**
   - Go to https://github.com/settings/developers
   - Click "New OAuth App"
   - Fill in details:
     - Application name: `Kagenti MCP Demo`
     - Homepage URL: `http://localhost:30187`
     - Authorization callback URL: `http://localhost:30187/oauth/callback`
   - Click "Register application"

2. **Note Credentials**
   - Copy the **Client ID**
   - Generate and copy the **Client Secret**

3. **Update Configuration**
   
   Edit `k8s/envoy-demo/02-mcp-server.yaml`:
   ```yaml
   env:
     - name: OAUTH_CLIENT_ID
       value: "Ov23liXXXXXXXXXXXXXX"  # Your Client ID
     - name: OAUTH_CLIENT_SECRET
       value: "your-client-secret-here"  # Your Client Secret
     - name: OAUTH_REDIRECT_URI
       value: "http://localhost:30187/oauth/callback"
   ```

## Build Docker Images

### Option 1: Build All Images

```bash
# From repository root
cd /path/to/github-mcp-server

# Build Token Broker
docker build -t ghcr.io/github/github-mcp-server:token-broker \
  --target token-broker \
  -f Dockerfile .

# Build Backend (Envoy version)
docker build -t ghcr.io/github/github-mcp-server:backend-envoy \
  --target backend-envoy \
  -f Dockerfile .

# Build AI Agent
docker build -t ghcr.io/github/github-mcp-server:aiagent \
  --target aiagent \
  -f Dockerfile .

# Build MCP Server (if not using existing image)
docker build -t ghcr.io/github/github-mcp-server:latest \
  -f Dockerfile .
```

### Option 2: Use Existing Images

If images are already published to a registry, skip building and update manifests to use the correct image tags.

### Load Images into kind (if using kind)

```bash
# Load images into kind cluster
kind load docker-image ghcr.io/github/github-mcp-server:token-broker
kind load docker-image ghcr.io/github/github-mcp-server:backend-envoy
kind load docker-image ghcr.io/github/github-mcp-server:aiagent
kind load docker-image ghcr.io/github/github-mcp-server:latest
```

## Deploy to Kubernetes

### Quick Deployment

Use the provided deployment script:

```bash
cd k8s/envoy-demo
./deploy.sh
```

### Manual Deployment

Deploy components in order:

```bash
cd k8s/envoy-demo

# 1. Create namespace
kubectl apply -f 00-namespace.yaml

# 2. Deploy Token Broker
kubectl apply -f 01-token-broker.yaml

# Wait for Token Broker to be ready
kubectl wait --for=condition=available --timeout=120s \
  deployment/token-broker -n kagenti-envoy-demo

# 3. Deploy MCP Server
kubectl apply -f 02-mcp-server.yaml

# Wait for MCP Server to be ready
kubectl wait --for=condition=available --timeout=120s \
  deployment/mcp-server -n kagenti-envoy-demo

# 4. Deploy Envoy ConfigMap
kubectl apply -f 03-envoy-config.yaml

# 5. Deploy AI Agent with Envoy sidecar
kubectl apply -f 04-aiagent.yaml

# Wait for AI Agent to be ready
kubectl wait --for=condition=available --timeout=120s \
  deployment/aiagent -n kagenti-envoy-demo

# 6. Deploy Backend
kubectl apply -f 05-backend.yaml

# Wait for Backend to be ready
kubectl wait --for=condition=available --timeout=120s \
  deployment/backend -n kagenti-envoy-demo
```

## Verify Deployment

### Check Pod Status

```bash
# All pods should be Running
kubectl get pods -n kagenti-envoy-demo

# Expected output:
# NAME                            READY   STATUS    RESTARTS   AGE
# token-broker-xxxxxxxxxx-xxxxx   1/1     Running   0          2m
# mcp-server-xxxxxxxxxx-xxxxx     1/1     Running   0          2m
# aiagent-xxxxxxxxxx-xxxxx        2/2     Running   0          1m
# backend-xxxxxxxxxx-xxxxx        1/1     Running   0          1m
```

### Check Services

```bash
kubectl get svc -n kagenti-envoy-demo

# Expected output:
# NAME                    TYPE        CLUSTER-IP      EXTERNAL-IP   PORT(S)          AGE
# token-broker-service    ClusterIP   10.96.x.x       <none>        8190/TCP         2m
# mcp-server-service      NodePort    10.96.x.x       <none>        8184:30184/TCP   2m
# aiagent-service         NodePort    10.96.x.x       <none>        8186:30186/TCP   1m
# backend-service         NodePort    10.96.x.x       <none>        8187:30187/TCP   1m
```

### Check Logs

```bash
# Token Broker logs
kubectl logs -n kagenti-envoy-demo -l app=token-broker --tail=50

# MCP Server logs
kubectl logs -n kagenti-envoy-demo -l app=mcp-server --tail=50

# AI Agent logs (both containers)
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c envoy --tail=50
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c aiagent --tail=50

# Backend logs
kubectl logs -n kagenti-envoy-demo -l app=backend --tail=50
```

### Health Checks

```bash
# Token Broker health
kubectl run test-pod --rm -it --image=curlimages/curl -n kagenti-envoy-demo -- \
  curl http://token-broker-service:8190/health

# Backend health
kubectl run test-pod --rm -it --image=curlimages/curl -n kagenti-envoy-demo -- \
  curl http://backend-service:8187/health
```

## Access the System

### Access Methods

#### Option 1: NodePort (kind/minikube)

Services are exposed via NodePort:

- **Backend/Demo**: http://localhost:30187/demo
- **MCP Server**: http://localhost:30184
- **AI Agent**: http://localhost:30186

#### Option 2: Port Forwarding

```bash
# Backend
kubectl port-forward -n kagenti-envoy-demo svc/backend-service 8187:8187

# Token Broker
kubectl port-forward -n kagenti-envoy-demo svc/token-broker-service 8190:8190

# MCP Server
kubectl port-forward -n kagenti-envoy-demo svc/mcp-server-service 8184:8184

# AI Agent
kubectl port-forward -n kagenti-envoy-demo svc/aiagent-service 8186:8186

# Envoy Admin
kubectl port-forward -n kagenti-envoy-demo -l app=aiagent 15000:15000
```

Then access:
- Demo: http://localhost:8187/demo
- Token Broker: http://localhost:8190
- Envoy Admin: http://localhost:15000

#### Option 3: Ingress (Production)

For production, set up an Ingress controller and create Ingress resources.

### Test the System

1. **Open Demo Page**
   ```
   http://localhost:30187/demo
   ```

2. **Submit a Task**
   - User ID: `test-user`
   - Task: `List my GitHub repositories`
   - Click "Submit Task"

3. **Complete OAuth Flow**
   - OAuth popup appears
   - Click "Authorize with GitHub"
   - Authorize the application
   - Redirected back to demo page
   - Task completes and shows results

## Troubleshooting

### Pods Not Starting

**Symptom**: Pods stuck in `Pending`, `CrashLoopBackOff`, or `Error` state

**Solutions**:

```bash
# Check pod details
kubectl describe pod -n kagenti-envoy-demo <pod-name>

# Check pod logs
kubectl logs -n kagenti-envoy-demo <pod-name>

# Check events
kubectl get events -n kagenti-envoy-demo --sort-by='.lastTimestamp'

# Common issues:
# - Image pull errors: Check image names and registry access
# - Resource limits: Increase CPU/memory limits
# - Init container failures: Check iptables setup (AI Agent pod)
```

### Envoy Configuration Errors

**Symptom**: AI Agent pod's envoy container failing

**Solutions**:

```bash
# Validate Envoy config
kubectl exec -n kagenti-envoy-demo -l app=aiagent -c envoy -- \
  /usr/local/bin/envoy --mode validate -c /etc/envoy/envoy.yaml

# Check Envoy logs
kubectl logs -n kagenti-envoy-demo -l app=aiagent -c envoy

# Check ConfigMap
kubectl get configmap envoy-config -n kagenti-envoy-demo -o yaml

# Common issues:
# - Invalid YAML syntax
# - Missing clusters or listeners
# - Incorrect service names
```

### Token Broker Connection Issues

**Symptom**: Backend or Envoy cannot reach Token Broker

**Solutions**:

```bash
# Test from within cluster
kubectl run test-pod --rm -it --image=curlimages/curl -n kagenti-envoy-demo -- \
  curl -v http://token-broker-service:8190/health

# Check Token Broker service
kubectl get svc token-broker-service -n kagenti-envoy-demo

# Check Token Broker endpoints
kubectl get endpoints token-broker-service -n kagenti-envoy-demo

# Common issues:
# - Service selector mismatch
# - Token Broker pod not running
# - Network policies blocking traffic
```

### OAuth Flow Not Working

**Symptom**: OAuth popup doesn't appear or authorization fails

**Solutions**:

```bash
# Check MCP Server OAuth configuration
kubectl get deployment mcp-server -n kagenti-envoy-demo -o yaml | grep -A 5 OAUTH

# Test OAuth discovery
kubectl run test-pod --rm -it --image=curlimages/curl -n kagenti-envoy-demo -- \
  curl http://mcp-server-service:8184/.well-known/oauth-protected-resource

# Check all component logs during OAuth flow
kubectl logs -n kagenti-envoy-demo -l app=token-broker -f &
kubectl logs -n kagenti-envoy-demo -l app=backend -f &
kubectl logs -n kagenti-envoy-demo -l app=mcp-server -f &

# Common issues:
# - Incorrect OAuth credentials
# - Wrong redirect URI
# - GitHub OAuth App not configured
# - Network connectivity to GitHub
```

### Session Timeout Issues

**Symptom**: Sessions expiring too quickly

**Solutions**:

```bash
# Check Token Broker configuration
kubectl get deployment token-broker -n kagenti-envoy-demo -o yaml | grep -A 5 TOKEN_BROKER

# Increase session timeout
kubectl set env deployment/token-broker \
  TOKEN_BROKER_SESSION_TIMEOUT=120s \
  -n kagenti-envoy-demo

# Check session timer logs
kubectl logs -n kagenti-envoy-demo -l app=token-broker | grep "Session timer"
```

### Performance Issues

**Symptom**: Slow response times or timeouts

**Solutions**:

```bash
# Check resource usage
kubectl top pods -n kagenti-envoy-demo

# Increase resource limits
kubectl edit deployment <deployment-name> -n kagenti-envoy-demo
# Update resources.limits and resources.requests

# Scale up replicas (Token Broker only for now)
kubectl scale deployment token-broker --replicas=3 -n kagenti-envoy-demo

# Check for network latency
kubectl run test-pod --rm -it --image=nicolaka/netshoot -n kagenti-envoy-demo -- \
  ping token-broker-service
```

## Cleanup

### Remove All Resources

```bash
# Using cleanup script
cd k8s/envoy-demo
./cleanup.sh

# Or manually
kubectl delete namespace kagenti-envoy-demo
```

### Remove kind Cluster

```bash
kind delete cluster
```

## Production Considerations

### Security

- [ ] Use Kubernetes Secrets for OAuth credentials
- [ ] Enable TLS/HTTPS for all services
- [ ] Implement network policies
- [ ] Use Pod Security Standards
- [ ] Enable audit logging
- [ ] Implement rate limiting

### Scalability

- [ ] Use HorizontalPodAutoscaler for Token Broker
- [ ] Implement Redis for shared token cache
- [ ] Use sticky sessions or session affinity
- [ ] Configure resource requests/limits appropriately
- [ ] Monitor and tune performance

### Reliability

- [ ] Set up health checks and readiness probes
- [ ] Configure PodDisruptionBudgets
- [ ] Implement graceful shutdown
- [ ] Set up monitoring and alerting
- [ ] Create runbooks for common issues
- [ ] Implement backup and disaster recovery

### Monitoring

- [ ] Deploy Prometheus for metrics
- [ ] Set up Grafana dashboards
- [ ] Configure log aggregation (ELK, Loki)
- [ ] Set up distributed tracing (Jaeger, Zipkin)
- [ ] Monitor Envoy statistics
- [ ] Track OAuth flow success rates

## Next Steps

After successful deployment:

1. Run complete test suite (see [TESTING.md](TESTING.md))
2. Set up monitoring and alerting
3. Configure production-grade security
4. Implement CI/CD pipeline
5. Create operational runbooks
6. Plan for scaling and high availability

## Support

For issues and questions:
- Check [TESTING.md](TESTING.md) for testing procedures
- Check [README.md](README.md) for architecture overview
- Review component-specific READMEs:
  - Token Broker: `cmd/token-broker/README.md`
  - Backend: `cmd/backend-envoy/README.md`