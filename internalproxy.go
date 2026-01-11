package driversdk

import (
	"context"
	"fmt"

	hostrpc "github.com/NotrixInc/nx-driver-sdk/hostrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// HostProxyClient is used by CHILD drivers to proxy to their HUB via driver-host.
type HostProxyClient struct {
	cc *grpc.ClientConn
	c  hostrpc.HostProxyServiceClient
}

func DialHostProxy(addr string) (*HostProxyClient, error) {
	// addr format: unix:////tmp/nxdriver-host.sock
	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	return &HostProxyClient{cc: conn, c: hostrpc.NewHostProxyServiceClient(conn)}, nil
}

func (h *HostProxyClient) Close() error { return h.cc.Close() }

func (h *HostProxyClient) ProxyCommand(ctx context.Context, hubDeviceID, childRef, endpointKey string, payload []byte, correlationID string) (CommandResult, error) {
	resp, err := h.c.ProxyCommand(ctx, &hostrpc.ProxyCommandRequest{
		HubDeviceId:   hubDeviceID,
		ChildRef:      childRef,
		EndpointKey:   endpointKey,
		Payload:       payload,
		CorrelationId: correlationID,
	})
	if err != nil {
		return CommandResult{Success: false, Message: err.Error()}, err
	}
	return CommandResult{Success: resp.Success, Message: resp.Message, Data: resp.Data}, nil
}

func RequireHostAddrFromEnv(getenv func(string) string) (string, error) {
	addr := getenv("NX_HOST_GRPC_ADDR")
	if addr == "" {
		return "", fmt.Errorf("NX_HOST_GRPC_ADDR is required")
	}
	return addr, nil
}
