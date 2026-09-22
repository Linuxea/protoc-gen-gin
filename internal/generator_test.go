package internal

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/compiler/protogen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

func mustGenerate(t *testing.T, fdp *descriptorpb.FileDescriptorProto) string {
	t.Helper()
	req := &pluginpb.CodeGeneratorRequest{
		FileToGenerate: []string{*fdp.Name},
		ProtoFile:      []*descriptorpb.FileDescriptorProto{fdp},
		Parameter:      proto.String("paths=source_relative"),
	}
	gen, err := protogen.Options{}.New(req)
	if err != nil {
		t.Fatalf("protogen.Options.New: %v", err)
	}
	for _, f := range gen.Files {
		if !f.Generate {
			continue
		}
		g := GenerateFile(gen, f)
		if g == nil {
			t.Fatal("GenerateFile returned nil")
		}
		buf, err := g.Content()
		if err != nil {
			t.Fatalf("Content: %v", err)
		}
		return string(buf)
	}
	t.Fatal("no file generated")
	return ""
}

func testFileDesc() *descriptorpb.FileDescriptorProto {
	return &descriptorpb.FileDescriptorProto{
		Name:    proto.String("user/v1/user.proto"),
		Package: proto.String("user.v1"),
		Options: &descriptorpb.FileOptions{
			GoPackage: proto.String("example.com/repo/proto/user/v1;userv1"),
		},
		Syntax: proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: proto.String("GetUserRequest")},
			{Name: proto.String("GetUserResponse")},
			{Name: proto.String("WatchRequest")},
			{Name: proto.String("WatchEvent")},
		},
		Service: []*descriptorpb.ServiceDescriptorProto{
			{
				Name: proto.String("UserService"),
				Method: []*descriptorpb.MethodDescriptorProto{
					{
						Name:       proto.String("GetUser"),
						InputType:  proto.String(".user.v1.GetUserRequest"),
						OutputType: proto.String(".user.v1.GetUserResponse"),
					},
					{
						Name:            proto.String("Watch"),
						InputType:       proto.String(".user.v1.WatchRequest"),
						OutputType:      proto.String(".user.v1.WatchEvent"),
						ServerStreaming: proto.Bool(true),
					},
				},
			},
		},
	}
}

func TestGenerateFileContent(t *testing.T) {
	content := mustGenerate(t, testFileDesc())

	for _, want := range []string{
		"package userv1",
		"func RegisterUserServiceGin(r gin.IRoutes, cli UserServiceClient)",
		"func UserServiceGetUserHandler(cli UserServiceClient) gin.HandlerFunc",
		"r.POST(\"/user.v1.UserService/GetUser\", UserServiceGetUserHandler(cli))",
		"// WARNING: streaming method Watch is not registered",
		"@Router",
		"/user.v1.UserService/GetUser [post]",
		"@Param",
		"body GetUserRequest true \"GetUser request body\"",
		"@Success",
		"200 {object} GetUserResponse",
		"@Failure",
		"400 {object} ginruntime.Error",
		"@ID",
		"user.v1.UserService.GetUser",
		"@Tags",
		"UserService",
		"ShouldBindJSON(&req)",
		"cli.GetUser(c.Request.Context(), &req)",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("generated content missing %q\n---\n%s", want, content)
		}
	}
}

func TestCommentLines(t *testing.T) {
	got := commentLines(protogen.CommentSet{Leading: "根据 ID 获取用户。\n多行说明第二行。\n"})
	want := []string{"根据 ID 获取用户。", "多行说明第二行。"}
	if len(got) != len(want) {
		t.Fatalf("commentLines() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("commentLines()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
