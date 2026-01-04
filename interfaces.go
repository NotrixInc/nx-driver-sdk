package driversdk

import "context"

// Base Driver interface (DEVICE/HUB/CHILD all implement)
type Driver interface {
	// Identity
	ID() string            // "com.vendor.product"
	Version() string       // "1.0.0"
	Type() DriverType      // DEVICE | HUB | CHILD
	Protocols() []Protocol // e.g. [IP] or [ZIGBEE]
	Topologies() []Topology

	// Lifecycle
	Init(ctx context.Context, deps Dependencies, cfg JSONConfig) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Health(ctx context.Context) (HealthStatus, map[string]string)

	// Control
	HandleCommand(ctx context.Context, cmd Command) (CommandResult, error)

	// Descriptors (host will upsert to controller-core)
	Endpoints() ([]Endpoint, error)
	Variables() ([]Variable, error)
}

// HUB driver: discovers and proxies non-IP network
type HubDriver interface {
	Driver
	DiscoverChildren(ctx context.Context) ([]ChildCandidate, error)
	ProxyCommand(ctx context.Context, childRef string, endpointKey string, payload []byte) (CommandResult, error)
}

// CHILD driver: controls device behind hub via hub proxy
type ChildDriver interface {
	Driver
	// Called by host to bind this child driver to a particular hub instance + child reference
	BindHub(ctx context.Context, hub HubProxy, childRef string) error
}

// HubProxy is provided by host to child drivers.
// Under the hood it routes to the correct HUB driver instance.
type HubProxy interface {
	ProxyCommand(ctx context.Context, childRef string, endpointKey string, payload []byte) (CommandResult, error)
	// Optional: subscribe to hub-native events; host can implement later
	// Subscribe(ctx context.Context, childRef string) (<-chan HubEvent, error)
}
