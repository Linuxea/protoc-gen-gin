package httpadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/reflection"
	"google.golang.org/grpc/test/bufconn"
)

const (
	healthService = "grpc.health.v1.Health"
	checkPath     = "/grpc.health.v1.Health/Check"
)

// newConn starts an in-process gRPC server exposing the standard health
// service (unary Check + streaming Watch) and server reflection.
func newConn(t *testing.T, opts ...grpc.ServerOption) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer(opts...)
	hs := health.NewServer()
	hs.SetServingStatus("up", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(srv, hs)
	reflection.Register(srv)
	t.Cleanup(srv.Stop)
	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.Dial("bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("grpc.Dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func do(t *testing.T, h http.Handler, method, path, body string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var got map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return w.Code, got
}

func newLocal(t *testing.T, opts ...Option) *Adapter {
	t.Helper()
	a := New(newConn(t), opts...)
	skipped, err := a.RegisterService(healthService)
	if err != nil {
		t.Fatalf("RegisterService: %v", err)
	}
	if len(skipped) != 1 || skipped[0] != "/grpc.health.v1.Health/Watch" {
		t.Fatalf("skipped = %v, want the streaming Watch method", skipped)
	}
	return a
}

func TestLocalRegistryOK(t *testing.T) {
	code, body := do(t, newLocal(t), http.MethodPost, checkPath, `{"service":"up"}`)
	if code != http.StatusOK || body["status"] != "SERVING" {
		t.Fatalf("got %d %v, want 200 SERVING", code, body)
	}
}

func TestGRPCErrorMapped(t *testing.T) {
	code, body := do(t, newLocal(t), http.MethodPost, checkPath, `{"service":"nope"}`)
	if code != http.StatusNotFound || body["code"] != float64(5) {
		t.Fatalf("got %d %v, want 404 / code 5", code, body)
	}
}

func TestEmptyBodyIsZeroRequest(t *testing.T) {
	// The empty service name is the server's overall status (SERVING by default).
	code, body := do(t, newLocal(t), http.MethodPost, checkPath, "")
	if code != http.StatusOK || body["status"] != "SERVING" {
		t.Fatalf("got %d %v, want 200 SERVING", code, body)
	}
}

func TestInvalidJSON(t *testing.T) {
	code, body := do(t, newLocal(t), http.MethodPost, checkPath, `{oops`)
	if code != http.StatusBadRequest || body["code"] != float64(3) {
		t.Fatalf("got %d %v, want 400 / code 3", code, body)
	}
}

func TestUnknownFieldIgnored(t *testing.T) {
	code, _ := do(t, newLocal(t), http.MethodPost, checkPath, `{"service":"up","extra":1}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
}

func TestStreamingNotRouted(t *testing.T) {
	code, _ := do(t, newLocal(t), http.MethodPost, "/grpc.health.v1.Health/Watch", `{}`)
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	code, _ := do(t, newLocal(t), http.MethodGet, checkPath, "")
	if code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", code)
	}
}

func TestBodyTooLarge(t *testing.T) {
	a := newLocal(t, WithMaxBodyBytes(8))
	code, _ := do(t, a, http.MethodPost, checkPath, `{"service":"a-long-name"}`)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", code)
	}
}

func TestPrefixAndRoutes(t *testing.T) {
	a := newLocal(t, WithPrefix("/api"))
	routes := a.Routes()
	if len(routes) != 1 || routes[0].Path != "/api"+checkPath || routes[0].FullMethod != checkPath {
		t.Fatalf("routes = %+v", routes)
	}
	// A route handler serves its own method whatever the mounted path is.
	code, body := do(t, routes[0].Handler, http.MethodPost, "/anything", `{"service":"up"}`)
	if code != http.StatusOK || body["status"] != "SERVING" {
		t.Fatalf("got %d %v, want 200 SERVING", code, body)
	}
}

func TestMetadataForwarded(t *testing.T) {
	var got []string
	conn := newConn(t, grpc.UnaryInterceptor(func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, h grpc.UnaryHandler) (interface{}, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		got = md.Get("authorization")
		return h(ctx, req)
	}))
	a := New(conn, WithMetadata(func(r *http.Request) metadata.MD {
		return metadata.Pairs("authorization", r.Header.Get("Authorization"))
	}))
	if _, err := a.RegisterService(healthService); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, checkPath, bytes.NewBufferString(`{"service":"up"}`))
	req.Header.Set("Authorization", "Bearer t")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, req)
	if w.Code != http.StatusOK || len(got) != 1 || got[0] != "Bearer t" {
		t.Fatalf("status %d, forwarded %v", w.Code, got)
	}
}

func TestReflectionDynamic(t *testing.T) {
	conn := newConn(t)
	files, services, err := FetchFiles(context.Background(), conn)
	if err != nil {
		t.Fatalf("FetchFiles: %v", err)
	}
	if len(services) != 1 || services[0] != healthService {
		t.Fatalf("services = %v, want only %s", services, healthService)
	}
	a := New(conn, WithFiles(files))
	if _, err := a.RegisterService(services...); err != nil {
		t.Fatalf("RegisterService: %v", err)
	}
	// Descriptors from reflection are distinct from the linked-in ones, so
	// this exercises the dynamicpb path end to end.
	rt := a.Routes()[0]
	if messageType(rt.Method.Input()).New().Interface() == nil {
		t.Fatal("nil message")
	}
	code, body := do(t, a, http.MethodPost, checkPath, `{"service":"up"}`)
	if code != http.StatusOK || body["status"] != "SERVING" {
		t.Fatalf("got %d %v, want 200 SERVING", code, body)
	}
}

func TestUnknownService(t *testing.T) {
	a := New(newConn(t))
	if _, err := a.RegisterService("no.such.Service"); err == nil {
		t.Fatal("want error for unknown service")
	}
}
