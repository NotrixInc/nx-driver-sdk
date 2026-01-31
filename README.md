# NX Driver SDK (Go)

A minimal Go SDK that defines the driver contracts used by the Notrix driver-host / controller stack.

This repository **does not** include a runnable host or a sample driver binary. It provides:

- Public interfaces for implementing drivers (`Driver`, `HubDriver`, `ChildDriver`)
- Common types for commands, endpoints, variables, and events
- Host-provided dependency interfaces (`Publisher`, `Logger`, `Clock`)
- A tiny internal gRPC contract used for hub/child proxying (`hostrpc/*`)
- Optional HTTP helpers for driver-to-driver messaging via controller-core (`DriverMessageClient`)

## Requirements

- Go **1.22+**

## Install

```bash
go get github.com/NotrixInc/nx-driver-sdk
```

Import from your driver project:

```go
import driversdk "github.com/NotrixInc/nx-driver-sdk"
```

## Concepts

### Driver types

Drivers declare a `Type()`:

- `DEVICE`: a direct device driver (often IP)
- `HUB`: a hub/gateway driver that discovers and proxies non-IP devices
- `CHILD`: a driver that controls a device behind a hub via proxy

They also declare supported `Protocols()` (e.g. `IP`, `ZIGBEE`, `RS485`) and `Topologies()` (`DIRECT_IP`, `VIA_HUB`).

### Lifecycle and control

All drivers implement `Driver`:

- Identity: `ID()`, `Version()`, `Type()`, `Protocols()`, `Topologies()`
- Lifecycle: `Init(ctx, deps, cfg)`, `Start(ctx)`, `Stop(ctx)`, `Health(ctx)`
- Commands: `HandleCommand(ctx, cmd)`
- Descriptors: `Endpoints()` and `Variables()` (the host upserts these into the controller)

The host sends a `Command` with an `EndpointKey`, a `CorrelationID`, and an opaque JSON payload.

### Publishing state/events back to the host

At `Init`, the host provides `Dependencies`:

- `Publisher`: upsert descriptors and publish runtime updates
- `Logger`: logging interface
- `Clock`: time source

Drivers typically publish:

- `StateUpdate` via `PublishState`
- `VariableUpdate` via `PublishVariable`
- `DeviceEvent` via `PublishEvent`

### Configuration

The host provides config as opaque JSON (`JSONConfig`). Drivers should decode it into a typed struct:

```go
type Config struct {
	Host string `json:"host"`
	Token string `json:"token"`
}

func (d *MyDriver) Init(ctx context.Context, deps driversdk.Dependencies, cfg driversdk.JSONConfig) error {
	d.deps = deps
	var c Config
	if err := cfg.Decode(&c); err != nil {
		return err
	}
	d.cfg = c
	return nil
}
```

## Hub / Child model

### Hub drivers

Hub drivers add discovery and proxy capabilities:

- `DiscoverChildren(ctx)` returns a list of `ChildCandidate` records.
- `ProxyCommand(ctx, childRef, endpointKey, payload)` executes a command on a child device in the hub's namespace.

`ChildCandidate.ChildRef` is the stable identifier within the hub namespace (e.g., Zigbee IEEE address, RS-485 slave id).

### Child drivers

Child drivers are bound to a hub instance by the host:

- `BindHub(ctx, hubProxy, childRef)` is called by the host
- The child uses the provided `HubProxy` to forward protocol-native operations

## Internal proxying (gRPC)

This SDK includes an internal gRPC contract in `hostrpc/nx_internal.pb.go`.

It enables (typical flow):

1. CHILD driver calls the driver-host (gRPC) to request proxying (`HostProxyService/ProxyCommand`)
2. driver-host routes the request to the correct HUB driver instance
3. HUB driver replies with success/data, which is returned back to the CHILD

Helpers:

- `driversdk.RequireHostAddrFromEnv(os.Getenv)` reads `NX_HOST_GRPC_ADDR`
- `driversdk.DialHostProxy(addr)` creates a `HostProxyClient`
- `(*HostProxyClient).ProxyCommand(...)` sends `hubDeviceID`, `childRef`, `endpointKey`, and raw payload bytes

Note: The `DialHostProxy` comment mentions a `unix:` address format; on Windows, the host will typically provide a TCP address (for example `127.0.0.1:50051`) depending on your driver-host implementation.

## Driver-to-driver messaging (HTTP)

If your deployment enables it, drivers can exchange messages via controller-core using **driver IDs**.

- Rules are configured in controller-core using bindings with `binding_type="DRIVER_TO_DRIVER"`.
- Controller-core enforces a **strict allow-list**: if there is no matching allow rule, delivery is rejected.

Use `DriverMessageClient` (see `driver_messages.go`):

- `driversdk.NewDriverMessageClientFromEnv()` reads `CORE_HTTP_ADDR` or `CONTROLLER_CORE_HTTP_ADDR`.
- `Publish(ctx, sourceDriverID, targetDriverID, type, payload, correlationID)`
- `Poll(ctx, driverID, afterID, wait, limit)`

To broadcast, pass an empty `targetDriverID` to `Publish(...)` (controller-core delivers to all allowed targets).

## Minimal examples

### 1) DEVICE driver skeleton

```go
package mydriver

import (
	"context"
	"encoding/json"

	driversdk "github.com/NotrixInc/nx-driver-sdk"
)

type Driver struct {
	deps driversdk.Dependencies
}

func (d *Driver) ID() string            { return "com.example.mydevice" }
func (d *Driver) Version() string       { return "0.1.0" }
func (d *Driver) Type() driversdk.DriverType { return driversdk.DriverTypeDevice }
func (d *Driver) Protocols() []driversdk.Protocol { return []driversdk.Protocol{driversdk.ProtocolIP} }
func (d *Driver) Topologies() []driversdk.Topology { return []driversdk.Topology{driversdk.TopologyDirectIP} }

func (d *Driver) Init(ctx context.Context, deps driversdk.Dependencies, cfg driversdk.JSONConfig) error {
	d.deps = deps
	return nil
}
func (d *Driver) Start(ctx context.Context) error { return nil }
func (d *Driver) Stop(ctx context.Context) error  { return nil }

func (d *Driver) Health(ctx context.Context) (driversdk.HealthStatus, map[string]string) {
	return driversdk.HealthOK, map[string]string{}
}

func (d *Driver) Endpoints() ([]driversdk.Endpoint, error) {
	return []driversdk.Endpoint{
		{Key: "power", Name: "Power", Direction: "INPUT", Type: "switch"},
	}, nil
}

func (d *Driver) Variables() ([]driversdk.Variable, error) {
	return nil, nil
}

func (d *Driver) HandleCommand(ctx context.Context, cmd driversdk.Command) (driversdk.CommandResult, error) {
	switch cmd.EndpointKey {
	case "power":
		// Parse cmd.Payload and apply to device...
		_ = json.RawMessage(cmd.Payload)
		return driversdk.CommandResult{Success: true, Message: "ok"}, nil
	default:
		return driversdk.CommandResult{Success: false, Message: "unknown endpoint"}, nil
	}
}
```

### 2) CHILD driver using host proxy client

This is one way a child driver can forward a request to its hub via the driver-host.

```go
addr, err := driversdk.RequireHostAddrFromEnv(os.Getenv)
if err != nil { /* handle */ }

client, err := driversdk.DialHostProxy(addr)
if err != nil { /* handle */ }
defer client.Close()

res, err := client.ProxyCommand(ctx, hubDeviceID, childRef, "power", payloadBytes, correlationID)
```

## Repo layout

- `interfaces.go`: core driver interfaces (`Driver`, `HubDriver`, `ChildDriver`, `HubProxy`)
- `types.go`: shared types (commands, descriptors, updates, child candidates)
- `deps.go`: host dependency contracts (`Publisher`, `Logger`, `Clock`, `Dependencies`)
- `config.go`: JSON config wrapper (`JSONConfig`)
- `helpers.go`: local-dev helpers (`NewNoopPublisher`, `NewStdLogger`, `NewSystemClock`)
- `internalproxy.go`: child->host proxy client helpers (`HostProxyClient`)
- `hostrpc/nx_internal.pb.go`: minimal internal gRPC contract (handwritten)

## Local development tips

If you want to run driver code without a real host/controller:

- Use `driversdk.NewNoopPublisher()` to avoid needing controller-core
- Use `driversdk.NewStdLogger()` for simple stdout logging
- Use `driversdk.NewSystemClock()` as a basic time source

## License

See your organization policy/repo settings.