package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	userv1 "github.com/Linuxea/protoc-gen-gin/example/proto/user/v1"
)

func newEngine(t *testing.T) *gin.Engine {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	grpcSrv := grpc.NewServer()
	userv1.RegisterUserServiceServer(grpcSrv, &userService{users: map[int64]*userv1.GetUserResponse{
		1: {Id: 1, Name: "alice", Email: "alice@example.com"},
	}})
	userv1.RegisterNotificationServiceServer(grpcSrv, notificationService{})
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

	gin.SetMode(gin.TestMode)
	r := gin.New()
	userv1.RegisterUserServiceGin(r, userv1.NewUserServiceClient(conn))
	userv1.RegisterNotificationServiceGin(r, userv1.NewNotificationServiceClient(conn))
	return r
}

func do(t *testing.T, h http.Handler, method, path, body string) (int, map[string]any) {
	t.Helper()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var got map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	return w.Code, got
}

func TestGetUserOK(t *testing.T) {
	code, body := do(t, newEngine(t), http.MethodPost, "/user.v1.UserService/GetUser", `{"id":1}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %v", code, body)
	}
	if body["name"] != "alice" || body["email"] != "alice@example.com" {
		t.Fatalf("body = %v, want alice", body)
	}
}

func TestGetUserNotFound(t *testing.T) {
	code, body := do(t, newEngine(t), http.MethodPost, "/user.v1.UserService/GetUser", `{"id":99}`)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body = %v", code, body)
	}
	if body["code"] != float64(5) || body["message"] != "user 99 not found" {
		t.Fatalf("body = %v, want code 5 / user 99 not found", body)
	}
}

func TestInvalidJSON(t *testing.T) {
	code, body := do(t, newEngine(t), http.MethodPost, "/user.v1.UserService/GetUser", `{oops`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if body["code"] != float64(3) {
		t.Fatalf("body = %v, want grpc code 3", body)
	}
}

func TestEmptyBodyAllowed(t *testing.T) {
	code, body := do(t, newEngine(t), http.MethodPost, "/user.v1.UserService/UpdateUser", "")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
	if body["message"] != "id is required" {
		t.Fatalf("body = %v, want server-side invalid argument", body)
	}
}

func TestStreamingRouteNotRegistered(t *testing.T) {
	code, _ := do(t, newEngine(t), http.MethodPost, "/user.v1.UserService/WatchUsers", `{"id":1}`)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (streaming method must not be routed)", code)
	}
}

func TestNotificationSend(t *testing.T) {
	code, body := do(t, newEngine(t), http.MethodPost, "/user.v1.NotificationService/Send", `{"user_id":1,"content":"hi"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %v", code, body)
	}
	if body["message_id"] != "msg-1" {
		t.Fatalf("body = %v, want message_id msg-1", body)
	}
}
