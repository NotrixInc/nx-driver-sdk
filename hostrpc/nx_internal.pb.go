package hostrpc

import (
	"context"

	"google.golang.org/grpc"
)

// This file is intentionally handwritten to avoid protoc in the minimal reference.
// It defines a tiny internal gRPC contract for CHILD -> HOST -> HUB proxying.

type ProxyCommandRequest struct {
	HubDeviceId    string
	ChildRef       string
	EndpointKey    string
	Payload        []byte
	CorrelationId  string
}

type ProxyCommandResponse struct {
	Success bool
	Message string
	Data    []byte
	CorrelationId string
}

// HostProxyService: called by CHILD drivers (client) -> driver-host (server).
type HostProxyServiceClient interface {
	ProxyCommand(ctx context.Context, in *ProxyCommandRequest, opts ...grpc.CallOption) (*ProxyCommandResponse, error)
}

type hostProxyServiceClient struct{ cc grpc.ClientConnInterface }

func NewHostProxyServiceClient(cc grpc.ClientConnInterface) HostProxyServiceClient {
	return &hostProxyServiceClient{cc}
}

func (c *hostProxyServiceClient) ProxyCommand(ctx context.Context, in *ProxyCommandRequest, opts ...grpc.CallOption) (*ProxyCommandResponse, error) {
	out := new(ProxyCommandResponse)
	err := c.cc.Invoke(ctx, "/nx.internal.HostProxyService/ProxyCommand", in, out, opts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

type HostProxyServiceServer interface {
	ProxyCommand(context.Context, *ProxyCommandRequest) (*ProxyCommandResponse, error)
	mustEmbedUnimplementedHostProxyServiceServer()
}

type UnimplementedHostProxyServiceServer struct{}

func (UnimplementedHostProxyServiceServer) ProxyCommand(context.Context, *ProxyCommandRequest) (*ProxyCommandResponse, error) {
	return nil, grpc.Errorf(grpc.Code(grpc.ErrServerStopped), "method ProxyCommand not implemented")
}
func (UnimplementedHostProxyServiceServer) mustEmbedUnimplementedHostProxyServiceServer() {}

func RegisterHostProxyServiceServer(s grpc.ServiceRegistrar, srv HostProxyServiceServer) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "nx.internal.HostProxyService",
		HandlerType: (*HostProxyServiceServer)(nil),
		Methods: []grpc.MethodDesc{
			{
				MethodName: "ProxyCommand",
				Handler: func(_ interface{}, ctx context.Context, dec func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
					in := new(ProxyCommandRequest)
					if err := dec(in); err != nil {
						return nil, err
					}
					return srv.ProxyCommand(ctx, in)
				},
			},
		},
		Streams:  []grpc.StreamDesc{},
		Metadata: "nx_internal.proto",
	}, srv)
}

// HubGatewayService: hub drivers open a bidirectional stream to receive proxy requests and send responses.
type HubGatewayServiceClient interface {
	OpenHubSession(ctx context.Context, opts ...grpc.CallOption) (HubGatewayService_OpenHubSessionClient, error)
}

type hubGatewayServiceClient struct{ cc grpc.ClientConnInterface }

func NewHubGatewayServiceClient(cc grpc.ClientConnInterface) HubGatewayServiceClient {
	return &hubGatewayServiceClient{cc}
}

func (c *hubGatewayServiceClient) OpenHubSession(ctx context.Context, opts ...grpc.CallOption) (HubGatewayService_OpenHubSessionClient, error) {
	stream, err := c.cc.NewStream(ctx, &grpc.ServiceDesc{
		ServiceName: "nx.internal.HubGatewayService",
		Streams: []grpc.StreamDesc{{
			StreamName:    "OpenHubSession",
			ServerStreams: true,
			ClientStreams: true,
		}},
	}, "/nx.internal.HubGatewayService/OpenHubSession", opts...)
	if err != nil {
		return nil, err
	}
	return &hubGatewayServiceOpenHubSessionClient{stream}, nil
}

type HubGatewayService_OpenHubSessionClient interface {
	Send(*ProxyCommandResponse) error
	Recv() (*ProxyCommandRequest, error)
	grpc.ClientStream
}

type hubGatewayServiceOpenHubSessionClient struct{ grpc.ClientStream }

func (x *hubGatewayServiceOpenHubSessionClient) Send(m *ProxyCommandResponse) error { return x.ClientStream.SendMsg(m) }
func (x *hubGatewayServiceOpenHubSessionClient) Recv() (*ProxyCommandRequest, error) {
	m := new(ProxyCommandRequest)
	if err := x.ClientStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}

type HubGatewayServiceServer interface {
	OpenHubSession(HubGatewayService_OpenHubSessionServer) error
	mustEmbedUnimplementedHubGatewayServiceServer()
}

type UnimplementedHubGatewayServiceServer struct{}

func (UnimplementedHubGatewayServiceServer) OpenHubSession(HubGatewayService_OpenHubSessionServer) error {
	return grpc.Errorf(grpc.Code(grpc.ErrServerStopped), "method OpenHubSession not implemented")
}
func (UnimplementedHubGatewayServiceServer) mustEmbedUnimplementedHubGatewayServiceServer() {}

type HubGatewayService_OpenHubSessionServer interface {
	Send(*ProxyCommandRequest) error
	Recv() (*ProxyCommandResponse, error)
	grpc.ServerStream
}

func RegisterHubGatewayServiceServer(s grpc.ServiceRegistrar, srv HubGatewayServiceServer) {
	s.RegisterService(&grpc.ServiceDesc{
		ServiceName: "nx.internal.HubGatewayService",
		HandlerType: (*HubGatewayServiceServer)(nil),
		Streams: []grpc.StreamDesc{
			{
				StreamName:    "OpenHubSession",
				Handler:       _HubGatewayService_OpenHubSession_Handler(srv),
				ServerStreams: true,
				ClientStreams: true,
			},
		},
		Metadata: "nx_internal.proto",
	}, srv)
}

func _HubGatewayService_OpenHubSession_Handler(srv HubGatewayServiceServer) grpc.StreamHandler {
	return func(srvIface interface{}, stream grpc.ServerStream) error {
		return srv.OpenHubSession(&hubGatewayServiceOpenHubSessionServer{stream})
	}
}

type hubGatewayServiceOpenHubSessionServer struct{ grpc.ServerStream }

func (x *hubGatewayServiceOpenHubSessionServer) Send(m *ProxyCommandRequest) error { return x.ServerStream.SendMsg(m) }
func (x *hubGatewayServiceOpenHubSessionServer) Recv() (*ProxyCommandResponse, error) {
	m := new(ProxyCommandResponse)
	if err := x.ServerStream.RecvMsg(m); err != nil {
		return nil, err
	}
	return m, nil
}
