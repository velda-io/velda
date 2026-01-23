# Pool Autoscaling Configuration

Autoscaling controls how Velda automatically adjusts the number of worker agents in a pool based on workload demand. This guide explains the autoscaling parameters and strategies.

## Overview

The autoscaler maintains a pool of worker agents, automatically:

- **Scaling up**: Creating new workers when demand exceeds available capacity
- **Scaling down**: Removing idle workers to minimize costs
- **Maintaining availability**: Keeping a buffer of idle workers for fast job starts

## Autoscaling Configuration

The autoscaler configuration is defined in the `auto_scaler` section of each pool:

```yaml
agent_pools:
  - name: "example-pool"
    auto_scaler:
      backend: # See Pool Backends reference
      max_agents: 20
      min_agents: 0
      min_idle_agents: 2
      max_idle_agents: 5
      default_slots_per_agent: 1
      idle_decay: "5m"
      sync_loop_interval: "60s"
      kill_unknown_after: "10m"
      mode: MODE_STANDARD
      metadata:
        description: "Example GPU pool for ML training"
```

## Core Parameters

### Size Limits

#### `max_agents` (required)

Maximum number of agents allowed in the pool. This is a **hard upper limit** to prevent runaway scaling and control costs.

```yaml
max_agents: 20  # Never create more than 20 agents
```

**Best Practice**: Set this based on your budget and infrastructure limits. Consider the maximum concurrent workloads you expect.

#### `min_agents` (optional, default: 0)

Minimum number of agents to maintain in the pool. The autoscaler will **never** scale below this number, even if all agents are idle.

```yaml
min_agents: 2  # Always keep at least 2 agents running
```

**Use Cases**:
- Ensure minimum availability for critical workloads
- Pre-warm capacity for predictable workload patterns
- Avoid cold start penalties

**Cost Implication**: Agents count toward your infrastructure costs even when idle.

### Idle Agent Management

#### `min_idle_agents` (optional, default: 0)

Minimum number of **idle agents** (or slots) to maintain. The autoscaler provisions new agents when:

```
(total_idle_slots - pending_sessions) < min_idle_agents
```

```yaml
min_idle_agents: 3  # Keep 3 idle agents ready for immediate assignment
```

**Purpose**: Reduce job startup latency by pre-provisioning capacity.

#### `max_idle_agents` (optional, default: min_idle + default_slots_per_agent - 1)

Maximum number of idle agents allowed. When exceeded, the autoscaler begins removing idle agents.

```yaml
max_idle_agents: 10  # Remove agents if more than 10 are idle
```

**Best Practice**: Set `max_idle_agents` > `min_idle_agents` to provide a buffer before aggressive scale-down.

#### `default_slots_per_agent` (optional, default: 1)

Number of concurrent sessions each agent can handle. This affects idle capacity calculations.

Note concurrent session feature is experimental: Currently they may share the same IP address.

```yaml
default_slots_per_agent: 4  # Each agent can run 4 sessions
```

**Important**: 
- Idle capacity is measured in **slots**, not agents
- An agent with 4 slots running 2 sessions contributes 2 idle slots
- Must match the agent's configured `max_sessions` in the agent config

**Validation**: The autoscaler automatically adjusts `max_idle_agents` if it's too low:

```
max_idle_agents >= min_idle_agents + default_slots_per_agent - 1
```

## Scaling Behavior

### `idle_decay` (optional, default: 0s)

How long to wait before removing an idle agent when `min_idle_agents < idle_agents <= max_idle_agents`.

```yaml
idle_decay: "5m"  # Wait 5 minutes before removing excess idle agents
```

- **0s** (default): Remove idle agents immediately until min_idle_agents is reached.
- **Positive duration**: Add delay to prevent thrashing during bursty workloads

**Example Scenario**:
```yaml
min_idle_agents: 2
max_idle_agents: 5
idle_decay: "10m"
```

1. Pool has 8 idle agents (exceeds max_idle by 3)
2. Autoscaler waits 10 minutes
3. If still over max_idle, removes 3 agents

**Use Case**: Handle unresponsive workers in large pools without blocking autoscaling.

### `sync_loop_interval` (optional, default: 60s)

How often to scan the backend infrastructure to reconcile state.

```yaml
sync_loop_interval: "120s"  # Check backend every 2 minutes
```

During each sync, the autoscaler:
1. Lists all workers from the backend
2. Identifies unknown/zombie workers
3. Removes terminated workers from tracking
4. Requests deletion of workers exceeding `kill_unknown_after`

**Best Practice**: 
- **Fast loops (30s-60s)**: Quick detection of issues, more API calls
- **Slow loops (2m-5m)**: Reduced API costs, slower problem detection

### `kill_unknown_after` (optional, default: 0 = disabled)

Maximum time to wait for an "unknown" worker to reconnect before requesting deletion.

```yaml
kill_unknown_after: "15m"  # Delete workers that haven't connected in 15 minutes
```

**Unknown Workers** are:
- Found in the backend listing
- Not currently connected to the API server  
- No previous connection history with the scheduler

**Use Case**: Clean up stuck instances from failed provisioning or network issues.

## Scaling Modes

### `mode` (optional, default: MODE_STANDARD)

Controls the scheduling and lifecycle strategy.

#### MODE_STANDARD (Default)

Workers are provisioned one-by-one as needed and remain available for multiple jobs.

```yaml
mode: MODE_STANDARD
```


#### MODE_BATCH

If enabled and supported by the backend, it will enable batch mode request to scale up N agents at one time.

This is useful for gang-scheduling where you expect all or nothing scaling, or when needs topology requirement.

The allocation is per-job, and worker will not be reused.

```yaml
mode: MODE_BATCH
```


## Pool Metadata

### `metadata` (optional)

Descriptive information about the pool.

```yaml
metadata:
  description: "High-memory CPU pool for data processing jobs"
```

**Usage**: 
- Documentation in pool listings
- UI display
- Operational context

## Configuration Examples

### High-Availability Pool

Keep workers always ready with minimal latency:

```yaml
- name: "ha-gpu-pool"
  auto_scaler:
    backend:
      aws_launch_template:
        instance_type: "p3.2xlarge"
        # ... other backend config
    max_agents: 50
    min_agents: 10        # Always keep 10 running
    min_idle_agents: 5    # Keep 5 idle for instant allocation
    max_idle_agents: 15   # Allow buffer before scale-down
    idle_decay: "10m"     # Don't aggressively remove idle agents
```

### Cost-Optimized Pool

Minimize costs by scaling to zero when idle:

```yaml
- name: "batch-processing"
  auto_scaler:
    backend:
      aws_launch_template:
        instance_type: "c5.4xlarge"
        max_stopped_instances: 3  # Reuse stopped instances
        # ... other backend config
    max_agents: 100
    min_agents: 0               # Scale to zero when idle
    min_idle_agents: 0          # No pre-warmed capacity
    max_idle_agents: 2          # Minimal buffer
    idle_decay: "0s"            # Remove idle immediately
    sync_loop_interval: "30s"   # Quick cleanup
```

### Burst-Friendly Pool

Handle traffic spikes while controlling costs:

```yaml
- name: "web-inference"
  auto_scaler:
    backend:
      aws_launch_template:
        instance_type: "g4dn.xlarge"
        # ... other backend config
    max_agents: 40
    min_agents: 2               # Baseline capacity
    min_idle_agents: 3          # Quick response to new requests
    max_idle_agents: 10         # Allow burst buffer
    idle_decay: "5m"            # Wait before scale-down
    default_slots_per_agent: 4  # Each agent handles 4 concurrent jobs
```

### Multi-Session Pool

Run multiple sessions per agent:

```yaml
- name: "cpu-pool"
  auto_scaler:
    backend:
      aws_launch_template:
        instance_type: "c5.9xlarge"
        agent_config:
          daemon_config:
            max_sessions: 8   # Agent can run 8 sessions
    max_agents: 20
    min_idle_agents: 16         # 16 idle slots (e.g., 2 agents)
    max_idle_agents: 32         # 32 idle slots (e.g., 4 agents)  
    default_slots_per_agent: 8  # Must match max_sessions
```

## Monitoring and Troubleshooting

### Key Metrics to Monitor

1. **Pool Size**: Total agents (idle + running + pending)
2. **Idle Capacity**: Available slots for new jobs
3. **Pending Sessions**: Sessions waiting for agents
4. **Unknown Workers**: Workers in backend but not connected
5. **Scale-up Rate**: How fast new workers are provisioned
6. **Scale-down Rate**: How fast idle workers are removed

## Advanced Topics

### Instance Reuse

Some backends support keeping instances in a paused/stopped state for faster restarts:

- **AWS**: `max_stopped_instances`, `max_instance_lifetime`
- **Nebius**: `max_disk_pool_size`
- **Mithril**: `max_suspended_bids`

This reduces startup time from minutes to seconds at the cost of slightly higher complexity and some storage cost when instance is stopped.

### Batch Mode Details

Batch mode is designed for gang scheduling scenarios where all workers must:
- Start together
- Have specific placement (same rack, same region, etc.)
- Terminate together after job completion

The backend must implement `RequestBatch(ctx, count, label)` to support this mode.
