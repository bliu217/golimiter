# Kubernetes

Local **Kind** first, then the same manifests on **EKS**. Terraform can later own VPC, EKS, ECR, and ElastiCache; these YAML files stay the workload layer.

Compose on a laptop is unchanged. Kind is an extra path, not a replacement.

## Layout

| Path | Role |
|---|---|
| [base](base) | Namespace, ConfigMap, Redis StatefulSet + ClusterIP Service, limiter Deployment + ClusterIP Service, optional sim Job |
| [overlays/kind](overlays/kind) | Local images (`imagePullPolicy: Never`) |
| [overlays/eks](overlays/eks) | ECR image names, `gp3` PVC, internal NLB on the limiter Service |

## Objects (Compose → Kubernetes)

| Compose | Kubernetes |
|---|---|
| Compose project | Namespace `golimiter` |
| `limiter` × 3 | Deployment `limiter` (3 replicas) |
| published `50051` | Service `limiter` (first load balancer: ClusterIP, later NLB on EKS) |
| `redis` + volume | StatefulSet `redis` + PVC |
| bind-mount YAML | ConfigMap `limiter-config` |
| `docker compose run sim` | Job `sim` |

The limiter Service is the in-cluster load balancer. Clients dial `limiter:50051`; kube-proxy sends **new TCP connections** to ready pods. Redis stays on a private ClusterIP (`redis:6379`) — never a public LoadBalancer.

## Kind (laptop)

Requires Docker, [Kind](https://kind.sigs.k8s.io/), and `kubectl`.

```sh
kind create cluster --name golimiter

docker build --target limiter -t golimiter/limiter:dev .
docker build --target sim -t golimiter/sim:dev .
kind load docker-image golimiter/limiter:dev --name golimiter
kind load docker-image golimiter/sim:dev --name golimiter

kubectl apply -k deploy/k8s/overlays/kind
kubectl -n golimiter rollout status deployment/limiter
kubectl -n golimiter rollout status statefulset/redis
```

Kind nodes do not see laptop Docker images until you `kind load`.

Port-forward the **Service** (not a single pod) so the laptop hits the ClusterIP balancer:

```sh
kubectl -n golimiter port-forward svc/limiter 50051:50051
go run ./cmd/sim -addr localhost:50051 -requests 100 -concurrency 10 -reset
```

In-cluster sim Job (delete first if it already ran):

```sh
kubectl -n golimiter delete job sim --ignore-not-found
kubectl apply -k deploy/k8s/overlays/kind
kubectl -n golimiter logs job/sim
```

Cheatsheet:

```sh
kubectl -n golimiter get pods,svc,pvc
kubectl -n golimiter describe pod -l app=limiter
kubectl -n golimiter logs -l app=limiter --tail=50
kind delete cluster --name golimiter
```

## EKS (later, costs money)

Do not apply this overlay until you have a cluster. Control plane + nodes are billed while the cluster exists; tear it down when you are done.

1. **VPC + subnets** — nodes and load balancers. Terraform later.
2. **ECR** — push `limiter` and `sim`; edit `images:` in [overlays/eks/kustomization.yaml](overlays/eks/kustomization.yaml).
3. **EKS node group + EBS CSI** — needed for the Redis PVC (`gp3`). Fargate + EBS is harder.
4. `kubectl apply -k deploy/k8s/overlays/eks` — limiter Service becomes an **internal NLB** (TCP for gRPC). Redis stays ClusterIP.
5. Point sim at the NLB DNS name, or run the sim Job in-cluster (`-addr=limiter:50051`).

The limiter has no auth yet, so the NLB annotation uses an **internal** scheme. Do not make it internet-facing until you add credentials or a private network path.

## Terraform / ElastiCache (after this)

Suggested order: `vpc` → `eks` → `ecr` → optional `elasticache`. Apply Kustomize (or Helm wrapping these YAML files) for workloads. Swap in-cluster Redis for ElastiCache by changing `redis.addr` in the ConfigMap; keep the limiter Deployment. Enable AUTH/TLS when you do.

Do not put VPC IDs in Go or in `base/`.
