// Command protoc-gen-gin is a protoc plugin that generates gin HTTP adapters
// for gRPC services. Every unary RPC method becomes a same-name POST route at
// /<proto.package>.<Service>/<Method> that binds a JSON body and forwards the
// call through the gRPC client.
package main

import (
	"flag"
	"fmt"
	"os"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/types/pluginpb"

	"github.com/Linuxea/protoc-gen-gin/internal"
)

func main() {
	var showVersion bool
	flag.BoolVar(&showVersion, "version", false, "print the version and exit")
	flag.Parse()
	if showVersion {
		fmt.Printf("protoc-gen-gin %v\n", internal.Version)
		os.Exit(0)
	}

	protogen.Options{
		ParamFunc: flag.CommandLine.Set,
	}.Run(func(gen *protogen.Plugin) error {
		gen.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)
		for _, f := range gen.Files {
			if !f.Generate {
				continue
			}
			internal.GenerateFile(gen, f)
		}
		return nil
	})
}
