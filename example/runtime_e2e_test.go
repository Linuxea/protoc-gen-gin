package main

import (
	"context"
	"net"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/test/bufconn"

	userv1 "github.com/Linuxea/protoc-gen-gin/example/proto/user/v1"
	"github.com/Linuxea/protoc-gen-gin/httpadapter"
)

// newRuntimeEngine mounts the same services as newEngine, but through the
// runtime adapter instead of the generated *.gin.pb.go handlers. With
// useReflection the descriptors come from the server, not from userv1.
func newRuntimeEngine(t *testing.T, useReflection bool) *gin.Engine {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer()
	userv1.RegisterUserServiceServer(grpcSrv, &userService{users: map[int64]*userv1.GetUserResponse{
		1: {Id: 1, Name: "alice", Email: "alice@example.com"},
	}})
	userv1.RegisterNotificationServiceServer(grpcSrv, notificationService{})
	reflection.Register(grpcSrv)
	t.Cleanup(grpcSrv.Stop)
	go func() { _ = grpcSrv.Serve(lis) }()

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	services := []string{"user.v1.UserService", "user.v1.NotificationService"}
	var opts []httpadapter.Option
	if useReflection {
		files, _, err := httpadapter.FetchFiles(context.Background(), conn)
		if err != nil {
			t.Fatalf("FetchFiles: %v", err)
		}
		opts = append(opts, httpadapter.WithFiles(files))
	}
	a := httpadapter.New(conn, opts...)
	if _, err := a.RegisterService(services...); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, rt := range a.Routes() {
		r.POST(rt.Path, gin.WrapH(rt.Handler))
	}
	return r
}

func TestRuntimeAdapter(t *testing.T) {
	for _, tc := range []struct {
		name       string
		reflection bool
	}{{"registry", false}, {"reflection", true}} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRuntimeEngine(t, tc.reflection)

			code, body := do(t, r, http.MethodPost, "/user.v1.UserService/GetUser", `{"id":1}`)
			// protojson encodes int64 as a JSON string, unlike the generated
			// handlers (encoding/json emits a number).
			if code != http.StatusOK || body["name"] != "alice" || body["id"] != "1" {
				t.Fatalf("GetUser: %d %v", code, body)
			}

			code, body = do(t, r, http.MethodPost, "/user.v1.UserService/GetUser", `{"id":99}`)
			if code != http.StatusNotFound || body["code"] != float64(5) || body["message"] != "user 99 not found" {
				t.Fatalf("GetUser not found: %d %v", code, body)
			}

			code, body = do(t, r, http.MethodPost, "/user.v1.UserService/UpdateUser", "")
			if code != http.StatusBadRequest || body["message"] != "id is required" {
				t.Fatalf("UpdateUser empty body: %d %v", code, body)
			}

			code, _ = do(t, r, http.MethodPost, "/user.v1.UserService/WatchUsers", `{"id":1}`)
			if code != http.StatusNotFound {
				t.Fatalf("WatchUsers: %d, want 404", code)
			}

			code, body = do(t, r, http.MethodPost, "/user.v1.NotificationService/Send", `{"user_id":1,"content":"hi"}`)
			if code != http.StatusOK || body["message_id"] != "msg-1" {
				t.Fatalf("Send: %d %v", code, body)
			}
		})
	}
}
