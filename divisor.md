# Divisor Configuration Guide

Divisor is an optional component that provides IP rotation for Quotient runner containers. It creates a network interface with multiple rotating IP addresses and manages NAT rules to distribute outbound traffic across those addresses.

## Table of Contents

- [Overview](#overview)
- [When to Use Divisor](#when-to-use-divisor)
- [Prerequisites](#prerequisites)
- [Configuration](#configuration)
- [Environment Variables](#environment-variables)
- [How It Works](#how-it-works)
- [Deployment](#deployment)
- [Troubleshooting](#troubleshooting)

---

## Overview

Divisor solves a common problem in cybersecurity competitions: preventing blue teams from identifying and blocking the scoring engine based on its source IP address. By rotating the source IP addresses used by runner containers, Divisor makes it significantly harder to distinguish scoring traffic from other network activity.

**Key Features:**
- Creates a dedicated network interface with multiple IP addresses
- Automatically discovers Quotient runner containers
- Configures SNAT (Source NAT) rules to rotate outbound IPs
- Triggers reconfiguration at the end of each scoring round
- Randomizes source ports for additional obfuscation

---

## When to Use Divisor

**Use Divisor when:**
- Blue teams might block scoring engine IPs
- You want to simulate more realistic network traffic patterns
- Competition rules require IP rotation
- You need to distribute load across multiple source IPs

**Skip Divisor when:**
- Running a simple practice environment
- Network isolation already prevents teams from seeing scoring traffic
- Blue teams are not expected to implement network-level blocks

---

## Prerequisites

Before configuring Divisor, ensure:

1. **Host Network Access**: Divisor requires `network_mode: host` in Docker
2. **Privileged Mode**: Container must run with `privileged: true` to manage iptables
3. **Docker Socket**: Access to `/var/run/docker.sock` to discover runner containers
4. **Available Subnet**: A subnet range that doesn't conflict with existing networks
5. **Git Submodule**: The divisor submodule must be initialized:

```bash
git submodule update --init --recursive
```

---

## Configuration

Divisor is configured entirely through environment variables. These can be set in the `.env` file in your Quotient root directory.

### Minimal Configuration

Add these variables to your `.env` file:

```bash
# Divisor Configuration
REDIS_ADDR=quotient_redis:6379
REDIS_PASSWORD=your_redis_password
NUM_IPS=5
DESIRED_SUBNET=10.192.0.0/10
TARGET_SUBNETS=10.100.0.0/16
INTERFACE_NAME=divisor
```

### Configuration with Multiple Target Subnets

If your competition infrastructure spans multiple subnets:

```bash
REDIS_ADDR=quotient_redis:6379
REDIS_PASSWORD=your_redis_password
NUM_IPS=10
DESIRED_SUBNET=192.168.200.0/24
TARGET_SUBNETS=10.100.0.0/16,10.200.0.0/16,172.16.0.0/12
INTERFACE_NAME=divisor
```

---

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `REDIS_ADDR` | Yes | `localhost:6379` | Address of the Quotient Redis server |
| `REDIS_PASSWORD` | Yes | - | Password for Redis authentication |
| `NUM_IPS` | Yes | - | Number of IP addresses to create on the interface |
| `DESIRED_SUBNET` | Yes | - | CIDR subnet from which to allocate IPs |
| `TARGET_SUBNETS` | Yes | - | Comma-separated list of destination subnets |
| `INTERFACE_NAME` | No | `divisor` | Name for the network interface |

### Variable Details

#### REDIS_ADDR

The address where Divisor can reach the Redis server. Since Divisor uses host networking, use `localhost:6379` if Redis port is exposed to the host, or the actual host IP.

```bash
# If Redis is exposed on localhost
REDIS_ADDR=localhost:6379

# If using host IP
REDIS_ADDR=192.168.1.100:6379
```

#### REDIS_PASSWORD

Must match the `REDIS_PASSWORD` configured for the Quotient Redis container.

#### NUM_IPS

The number of IP addresses to configure on the divisor interface. This should typically match or exceed the number of runner replicas configured in `docker-compose.yml`.

```bash
# For 5 runner replicas
NUM_IPS=5

# For high-availability with extra IPs
NUM_IPS=10
```

#### DESIRED_SUBNET

The CIDR subnet from which Divisor will randomly select IP addresses. Choose a subnet that:
- Does not conflict with your existing network infrastructure
- Does not overlap with team networks
- Has enough addresses for `NUM_IPS`

```bash
# Large private subnet
DESIRED_SUBNET=10.192.0.0/10

# Smaller dedicated subnet
DESIRED_SUBNET=192.168.200.0/24
```

#### TARGET_SUBNETS

Comma-separated list of destination subnets that should use the divisor interface. This typically includes all team network ranges.

```bash
# Single team subnet
TARGET_SUBNETS=10.100.0.0/16

# Multiple team subnets
TARGET_SUBNETS=10.100.0.0/16,10.200.0.0/16

# Broad range covering all teams
TARGET_SUBNETS=10.0.0.0/8
```

#### INTERFACE_NAME

The name assigned to the network interface created by Divisor. The default `divisor` works for most deployments.

---

## How It Works

### Initialization

1. Divisor connects to Redis and subscribes to the `events` channel
2. Creates (or recreates) the network interface specified by `INTERFACE_NAME`
3. Assigns `NUM_IPS` random addresses from `DESIRED_SUBNET` to the interface
4. Each IP is validated via ICMP ping to ensure it's not already in use

### Round-Based Rotation

1. Quotient publishes a `round_finish` event to Redis at the end of each scoring round
2. Divisor receives the event and triggers reconfiguration
3. New random IPs are selected and assigned to the interface
4. NAT rules are updated for all runner containers

### NAT Rule Configuration

For each Quotient runner container:
1. Divisor discovers the container's IP address via Docker API
2. Creates SNAT rules mapping the container IP to a divisor interface IP
3. Rules target traffic destined for `TARGET_SUBNETS`
4. Source ports are randomized for additional obfuscation

### Container Discovery

Divisor automatically finds runner containers by matching the naming pattern `quotient-runner-*`. This aligns with how Docker Compose names replicated service containers.

---

## Deployment

### Docker Compose Configuration

The default `docker-compose.yml` includes Divisor configuration:

```yaml
divisor:
  build:
    context: divisor/
    dockerfile: Dockerfile
  restart: always
  env_file:
    - .env
  network_mode: host
  depends_on:
    redis:
      condition: service_healthy
  privileged: true
  volumes:
    - /var/run/docker.sock:/var/run/docker.sock:ro
```

### Enabling Divisor

1. Initialize the submodule:
   ```bash
   git submodule update --init --recursive
   ```

2. Add configuration to `.env`:
   ```bash
   # Add to existing .env file
   NUM_IPS=5
   DESIRED_SUBNET=10.192.0.0/10
   TARGET_SUBNETS=10.100.0.0/16
   INTERFACE_NAME=divisor
   ```

3. Start the stack:
   ```bash
   docker-compose up --build -d
   ```

### Disabling Divisor

To run Quotient without IP rotation, comment out or remove the divisor service from `docker-compose.yml`:

```yaml
# divisor:
#   build:
#     context: divisor/
#     dockerfile: Dockerfile
#   ...
```

---

## Troubleshooting

### Divisor Container Won't Start

**Check submodule initialization:**
```bash
ls -la divisor/
# Should contain Dockerfile, main.go, etc.

# If empty, initialize:
git submodule update --init --recursive
```

**Check environment variables:**
```bash
docker-compose config | grep -A 20 divisor
```

### IP Addresses Not Rotating

**Verify Redis connectivity:**
```bash
docker logs quotient-divisor-1 2>&1 | grep -i redis
```

**Check for round_finish events:**
```bash
docker exec quotient_redis redis-cli -a $REDIS_PASSWORD SUBSCRIBE events
# Start a scoring round and verify events are published
```

### NAT Rules Not Applied

**Check iptables on host:**
```bash
sudo iptables -t nat -L -n -v | grep -i snat
```

**Verify container discovery:**
```bash
docker ps --filter "name=quotient-runner" --format "{{.Names}}"
```

### Network Conflicts

**Symptoms:** Connectivity issues, duplicate IP errors

**Resolution:**
1. Choose a different `DESIRED_SUBNET` that doesn't overlap with:
   - Host network interfaces
   - Docker bridge networks
   - Team infrastructure subnets

2. Verify no conflicts:
   ```bash
   ip addr show
   ip route show
   ```

### Checking Divisor Logs

```bash
# View recent logs
docker logs quotient-divisor-1

# Follow logs in real-time
docker logs -f quotient-divisor-1

# Check for errors
docker logs quotient-divisor-1 2>&1 | grep -i error
```

---

## Example Configurations

### Small Competition (5 teams, single subnet)

```bash
REDIS_ADDR=localhost:6379
REDIS_PASSWORD=secretpassword
NUM_IPS=5
DESIRED_SUBNET=192.168.250.0/24
TARGET_SUBNETS=10.100.0.0/16
INTERFACE_NAME=divisor
```

### Large Competition (20 teams, multiple subnets)

```bash
REDIS_ADDR=localhost:6379
REDIS_PASSWORD=secretpassword
NUM_IPS=20
DESIRED_SUBNET=10.192.0.0/10
TARGET_SUBNETS=10.100.0.0/16,10.200.0.0/16,172.16.0.0/12
INTERFACE_NAME=divisor
```

### High-Availability Setup (extra IPs for failover)

```bash
REDIS_ADDR=localhost:6379
REDIS_PASSWORD=secretpassword
NUM_IPS=15  # 3x the number of runners
DESIRED_SUBNET=10.192.0.0/10
TARGET_SUBNETS=10.0.0.0/8
INTERFACE_NAME=divisor
```

---

## Security Considerations

- Divisor requires privileged access and host networking
- The Docker socket is mounted read-only to limit exposure
- Ensure `REDIS_PASSWORD` is strong and matches across all services
- The `DESIRED_SUBNET` should not be routable from team networks
- Monitor iptables rules for unexpected modifications
