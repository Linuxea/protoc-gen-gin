// Package runtime provides the shared runtime helpers used by code generated
// with protoc-gen-gin. Generated files must depend on this package.
package ginruntime

import (
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Error is the JSON error response body emitted by every generated handler.
type Error struct {
	Code    int32  `json:"code"`
	Message string `json:"message"`
}

// grpcToHTTP maps gRPC status codes to HTTP status codes, following the same
// convention as grpc-gateway.
var grpcToHTTP = map[codes.Code]int{
	codes.Canceled:           499,
	codes.Unknown:            http.StatusInternalServerError,
	codes.InvalidArgument:    http.StatusBadRequest,
	codes.DeadlineExceeded:   http.StatusGatewayTimeout,
	codes.NotFound:           http.StatusNotFound,
	codes.AlreadyExists:      http.StatusConflict,
	codes.PermissionDenied:   http.StatusForbidden,
	codes.ResourceExhausted:  http.StatusTooManyRequests,
	codes.FailedPrecondition: http.StatusBadRequest,
	codes.Aborted:            http.StatusConflict,
	codes.OutOfRange:         http.StatusBadRequest,
	codes.Unimplemented:      http.StatusNotImplemented,
	codes.Internal:           http.StatusInternalServerError,
	codes.Unavailable:        http.StatusServiceUnavailable,
	codes.DataLoss:           http.StatusInternalServerError,
	codes.Unauthenticated:    http.StatusUnauthorized,
}

// HTTPStatus converts a (possibly wrapped) gRPC error into the HTTP status
// code, the gRPC status code and the message used for the error response
// body. Errors that are not gRPC status errors are reported as
// codes.Unknown / HTTP 500.
func HTTPStatus(err error) (httpStatus int, grpcCode int32, message string) {
	if err == nil {
		return http.StatusOK, 0, ""
	}
	st, _ := status.FromError(err)
	httpStatus, ok := grpcToHTTP[st.Code()]
	if !ok {
		httpStatus = http.StatusInternalServerError
	}
	return httpStatus, int32(st.Code()), st.Message()
}
