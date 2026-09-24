// Package httpadapter is the runtime (no code generation) counterpart of
// protoc-gen-gin: it turns gRPC unary methods into HTTP JSON endpoints by
// reading protobuf descriptors at runtime and forwarding requests through
// grpc.ClientConnInterface.Invoke.
//
// Routes and error bodies match the generated adapters:
//
//	POST /<proto package>.<Service>/<Method>
//	error body: {"code": <grpc code>, "message": "..."} (see ginruntime.Error)
//
// The adapter is a plain http.Handler so this package does not depend on gin;
// mount it with gin.WrapH (see Routes).
//
// Descriptors can come from the binary's own registry (protoregistry.GlobalFiles,
// populated by importing the *.pb.go packages) or from a remote server via gRPC
// server reflection (see FetchFiles), in which case no generated code is needed
// at all and messages are handled with dynamicpb.
package httpadapter

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/Linuxea/protoc-gen-gin/ginruntime"
)

// defaultMaxBodyBytes caps request bodies; it matches grpc-go's default
// maximum receive message size.
const defaultMaxBodyBytes = 4 << 20

// Route is one HTTP endpoint exposed by the adapter.
type Route struct {
	// Path is the HTTP path, e.g. /user.v1.UserService/GetUser (plus prefix).
	Path string
	// FullMethod is the gRPC method name, e.g. /user.v1.UserService/GetUser.
	FullMethod string
	// Method is the descriptor of the RPC.
	Method protoreflect.MethodDescriptor
	// Handler serves only this route; it ignores the request path.
	Handler http.Handler
}

// Option configures an Adapter.
type Option func(*Adapter)

// WithPrefix mounts every route under prefix (e.g. "/api").
func WithPrefix(prefix string) Option {
	return func(a *Adapter) { a.prefix = prefix }
}

// WithFiles resolves service names in RegisterService against files instead
// of protoregistry.GlobalFiles. Use it with descriptors from FetchFiles.
func WithFiles(files *protoregistry.Files) Option {
	return func(a *Adapter) { a.files = files }
}

// WithMarshalOptions overrides the JSON encoding of responses. The default
// uses proto field names (snake_case), matching the generated adapters.
func WithMarshalOptions(o protojson.MarshalOptions) Option {
	return func(a *Adapter) { a.marshal = o }
}

// WithUnmarshalOptions overrides the JSON decoding of requests. The default
// ignores unknown fields, matching the generated adapters.
func WithUnmarshalOptions(o protojson.UnmarshalOptions) Option {
	return func(a *Adapter) { a.unmarshal = o }
}

// WithMetadata derives outgoing gRPC metadata from each HTTP request, e.g. to
// forward an Authorization header. By default no metadata is sent.
func WithMetadata(fn func(*http.Request) metadata.MD) Option {
	return func(a *Adapter) { a.metadata = fn }
}

// WithMaxBodyBytes limits the request body size (default 4 MiB). n <= 0
// disables the limit.
func WithMaxBodyBytes(n int64) Option {
	return func(a *Adapter) { a.maxBody = n }
}

// WithCallOptions appends grpc.CallOptions to every forwarded call.
func WithCallOptions(opts ...grpc.CallOption) Option {
	return func(a *Adapter) { a.callOpts = append(a.callOpts, opts...) }
}

// Adapter forwards HTTP JSON requests to gRPC unary methods. It is safe for
// concurrent use, including registering services while serving.
type Adapter struct {
	conn      grpc.ClientConnInterface
	prefix    string
	files     *protoregistry.Files
	marshal   protojson.MarshalOptions
	unmarshal protojson.UnmarshalOptions
	metadata  func(*http.Request) metadata.MD
	maxBody   int64
	callOpts  []grpc.CallOption

	mu     sync.RWMutex
	routes map[string]*Route
}

// New returns an Adapter that forwards calls over conn.
func New(conn grpc.ClientConnInterface, opts ...Option) *Adapter {
	a := &Adapter{
		conn:      conn,
		files:     protoregistry.GlobalFiles,
		marshal:   protojson.MarshalOptions{UseProtoNames: true},
		unmarshal: protojson.UnmarshalOptions{DiscardUnknown: true},
		maxBody:   defaultMaxBodyBytes,
		routes:    map[string]*Route{},
	}
	for _, o := range opts {
		o(a)
	}
	// Any fields must resolve against the same descriptors the adapter uses.
	if a.files != protoregistry.GlobalFiles {
		types := dynamicpb.NewTypes(a.files)
		if a.marshal.Resolver == nil {
			a.marshal.Resolver = types
		}
		if a.unmarshal.Resolver == nil {
			a.unmarshal.Resolver = types
		}
	}
	return a
}

// RegisterService registers every unary method of the named services, e.g.
// "user.v1.UserService". Streaming methods are skipped (as in the generated
// adapters); the skipped full method names are returned.
func (a *Adapter) RegisterService(names ...string) (skipped []string, err error) {
	for _, name := range names {
		d, err := a.files.FindDescriptorByName(protoreflect.FullName(name))
		if err != nil {
			return skipped, fmt.Errorf("httpadapter: service %q: %w", name, err)
		}
		sd, ok := d.(protoreflect.ServiceDescriptor)
		if !ok {
			return skipped, fmt.Errorf("httpadapter: %q is a %T, not a service", name, d)
		}
		skipped = append(skipped, a.RegisterServiceDescriptor(sd)...)
	}
	return skipped, nil
}

// RegisterServiceDescriptor registers every unary method of sd and returns
// the full method names of skipped streaming methods.
func (a *Adapter) RegisterServiceDescriptor(sd protoreflect.ServiceDescriptor) (skipped []string) {
	methods := sd.Methods()
	for i := 0; i < methods.Len(); i++ {
		md := methods.Get(i)
		fullMethod := "/" + string(sd.FullName()) + "/" + string(md.Name())
		if md.IsStreamingClient() || md.IsStreamingServer() {
			skipped = append(skipped, fullMethod)
			continue
		}
		rt := &Route{
			Path:       a.prefix + fullMethod,
			FullMethod: fullMethod,
			Method:     md,
		}
		rt.Handler = a.newHandler(rt, messageType(md.Input()), messageType(md.Output()))
		a.mu.Lock()
		a.routes[rt.Path] = rt
		a.mu.Unlock()
	}
	return skipped
}

// Routes returns the registered routes sorted by path. To mount on gin:
//
//	for _, rt := range a.Routes() {
//		r.POST(rt.Path, gin.WrapH(rt.Handler))
//	}
//
// Alternatively mount the whole adapter with a catch-all route, which also
// picks up services registered later:
//
//	r.POST("/*rpc", gin.WrapH(a))
func (a *Adapter) Routes() []Route {
	a.mu.RLock()
	out := make([]Route, 0, len(a.routes))
	for _, rt := range a.routes {
		out = append(out, *rt)
	}
	a.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// ServeHTTP dispatches on the request path. Unknown paths get 404 and
// non-POST requests 405, both with a ginruntime.Error body.
func (a *Adapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	rt, ok := a.routes[r.URL.Path]
	a.mu.RUnlock()
	if !ok {
		writeError(w, http.StatusNotFound, codes.Unimplemented, "unknown method "+r.URL.Path)
		return
	}
	rt.Handler.ServeHTTP(w, r)
}

func (a *Adapter) newHandler(rt *Route, in, out protoreflect.MessageType) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeError(w, http.StatusMethodNotAllowed, codes.Unimplemented, r.Method+" not allowed, use POST")
			return
		}

		body := r.Body
		if a.maxBody > 0 {
			body = http.MaxBytesReader(w, body, a.maxBody)
		}
		raw, err := io.ReadAll(body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, codes.ResourceExhausted, "request body too large")
				return
			}
			writeError(w, http.StatusBadRequest, codes.InvalidArgument, "invalid request: "+err.Error())
			return
		}

		req := in.New().Interface()
		// An empty body is the zero-value request, like the generated adapters.
		if len(raw) > 0 {
			if err := a.unmarshal.Unmarshal(raw, req); err != nil {
				writeError(w, http.StatusBadRequest, codes.InvalidArgument, "invalid request: "+err.Error())
				return
			}
		}

		ctx := r.Context()
		if a.metadata != nil {
			if md := a.metadata(r); len(md) > 0 {
				ctx = metadata.NewOutgoingContext(ctx, md)
			}
		}
		resp := out.New().Interface()
		if err := a.conn.Invoke(ctx, rt.FullMethod, req, resp, a.callOpts...); err != nil {
			httpStatus, code, message := ginruntime.HTTPStatus(err)
			writeJSON(w, httpStatus, ginruntime.Error{Code: code, Message: message})
			return
		}

		b, err := a.marshal.Marshal(resp)
		if err != nil {
			writeError(w, http.StatusInternalServerError, codes.Internal, "encode response: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(b)
	})
}

// messageType prefers the generated Go type when it is linked into the binary
// and falls back to dynamicpb, so reflection-only services work too.
func messageType(md protoreflect.MessageDescriptor) protoreflect.MessageType {
	if mt, err := protoregistry.GlobalTypes.FindMessageByName(md.FullName()); err == nil &&
		mt.Descriptor() == md {
		return mt
	}
	return dynamicpb.NewMessageType(md)
}

// Compile-time check: dynamicpb messages must satisfy proto.Message so the
// grpc codec can marshal them.
var _ proto.Message = (*dynamicpb.Message)(nil)
