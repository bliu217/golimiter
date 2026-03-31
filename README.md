# Overview
## Purpose
Create a distributed rate-limiting service for gRPC calls  
Create a simulator in Go as a test harness for the service  
Create a simple UI to call the simulator with custom parameters  
Load test and expose metrics for the service  

## Learning points
- Concurrency safety in Go
  - Performance of goroutines vs threads
- Rate limiting algorithms
- Distributed systems
- System design
- Load testing and performance monitoring
  - utilizing my own simulator and k6 to expose different metrics
- In-memory caching for Redis optimization
- Fallback for Redis downtime
# Architecture 
  
## Tech Stack
Go - Simulator Client, gRPC Service  
NGINX - Load Balancer  
Docker - Multiple nodes  
Redis - Shared state + atomic coordination across nodes  
k6 - Load testing  
React - Simple UI for simulator  
Prometheus/Grafana - Metrics + UI  

## UML


# Metrics
## Simulator

## k6
