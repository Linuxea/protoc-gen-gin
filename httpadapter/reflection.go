package httpadapter

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	rpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// FetchFiles downloads descriptors from a server that has gRPC server
// reflection (v1) enabled and returns them as a registry for WithFiles, along
// with the advertised service names (the reflection service itself excluded).
//
// With this the adapter needs no generated code for the target services:
//
//	files, services, err := httpadapter.FetchFiles(ctx, conn)
//	a := httpadapter.New(conn, httpadapter.WithFiles(files))
//	_, err = a.RegisterService(services...)
func FetchFiles(ctx context.Context, conn grpc.ClientConnInterface) (*protoregistry.Files, []string, error) {
	stream, err := rpb.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("httpadapter: open reflection stream: %w", err)
	}
	defer func() { _ = stream.CloseSend() }()

	ask := func(req *rpb.ServerReflectionRequest) (*rpb.ServerReflectionResponse, error) {
		if err := stream.Send(req); err != nil {
			return nil, err
		}
		resp, err := stream.Recv()
		if err != nil {
			return nil, err
		}
		if e := resp.GetErrorResponse(); e != nil {
			return nil, fmt.Errorf("reflection error %d: %s", e.GetErrorCode(), e.GetErrorMessage())
		}
		return resp, nil
	}

	resp, err := ask(&rpb.ServerReflectionRequest{
		MessageRequest: &rpb.ServerReflectionRequest_ListServices{ListServices: ""},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("httpadapter: list services: %w", err)
	}

	seen := map[string]*descriptorpb.FileDescriptorProto{}
	add := func(resp *rpb.ServerReflectionResponse) error {
		for _, raw := range resp.GetFileDescriptorResponse().GetFileDescriptorProto() {
			fd := &descriptorpb.FileDescriptorProto{}
			if err := proto.Unmarshal(raw, fd); err != nil {
				return err
			}
			seen[fd.GetName()] = fd
		}
		return nil
	}

	var services []string
	for _, s := range resp.GetListServicesResponse().GetService() {
		name := s.GetName()
		if name == "grpc.reflection.v1.ServerReflection" || name == "grpc.reflection.v1alpha.ServerReflection" {
			continue
		}
		services = append(services, name)
		r, err := ask(&rpb.ServerReflectionRequest{
			MessageRequest: &rpb.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: name},
		})
		if err != nil {
			return nil, nil, fmt.Errorf("httpadapter: file containing %s: %w", name, err)
		}
		if err := add(r); err != nil {
			return nil, nil, fmt.Errorf("httpadapter: decode descriptor of %s: %w", name, err)
		}
	}

	// Servers usually send transitive dependencies along; fetch any that are
	// still missing by file name.
	for {
		var missing []string
		for _, fd := range seen {
			for _, dep := range fd.GetDependency() {
				if _, ok := seen[dep]; !ok {
					missing = append(missing, dep)
				}
			}
		}
		if len(missing) == 0 {
			break
		}
		for _, dep := range missing {
			if _, ok := seen[dep]; ok {
				continue
			}
			r, err := ask(&rpb.ServerReflectionRequest{
				MessageRequest: &rpb.ServerReflectionRequest_FileByFilename{FileByFilename: dep},
			})
			if err != nil {
				return nil, nil, fmt.Errorf("httpadapter: file %s: %w", dep, err)
			}
			if err := add(r); err != nil {
				return nil, nil, fmt.Errorf("httpadapter: decode descriptor %s: %w", dep, err)
			}
			if _, ok := seen[dep]; !ok {
				return nil, nil, fmt.Errorf("httpadapter: server did not return %s", dep)
			}
		}
	}

	set := &descriptorpb.FileDescriptorSet{}
	for _, fd := range seen {
		set.File = append(set.File, fd)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, nil, fmt.Errorf("httpadapter: build registry: %w", err)
	}
	return files, services, nil
}
