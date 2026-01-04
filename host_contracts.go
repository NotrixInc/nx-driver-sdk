package driversdk

// Host-side contract notes:
//
// The driver-host is responsible for constructing a HubProxy implementation and passing it
// into a CHILD driver via BindHub(ctx, hubProxy, childRef).
//
// A typical implementation will route ProxyCommand() calls to the running HUB driver instance
// associated with the CHILD's parent hub device.
//
// Recommended routing identity:
// - Hub device is identified by core hub device UUID (hubDeviceID).
// - Child device is identified within the hub namespace by childRef (stored in device.external_id).
//
// Suggested host implementation pattern:
//
// type hubProxy struct {
//     hubDeviceID string
//     router HubRouter // your internal interface that can reach the HUB instance
// }
//
// func (p *hubProxy) ProxyCommand(ctx context.Context, childRef, endpointKey string, payload []byte) (CommandResult, error) {
//     return p.router.ProxyCommandToHub(ctx, p.hubDeviceID, childRef, endpointKey, payload)
// }
//
// This file is documentation-only (no runtime code required).
