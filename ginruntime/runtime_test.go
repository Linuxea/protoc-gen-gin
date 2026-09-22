package ginruntime

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestHTTPStatus(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		httpStatus int
		grpcCode   int32
		message    string
	}{
		{"nil", nil, http.StatusOK, 0, ""},
		{"not found", status.Error(codes.NotFound, "user 42 not found"), http.StatusNotFound, 5, "user 42 not found"},
		{"invalid argument", status.Error(codes.InvalidArgument, "bad input"), http.StatusBadRequest, 3, "bad input"},
		{"unauthenticated", status.Error(codes.Unauthenticated, "who?"), http.StatusUnauthorized, 16, "who?"},
		{"unavailable", status.Error(codes.Unavailable, "later"), http.StatusServiceUnavailable, 14, "later"},
		{"deadline", status.Error(codes.DeadlineExceeded, "slow"), http.StatusGatewayTimeout, 4, "slow"},
		{"wrapped grpc error", fmt.Errorf("call failed: %w", status.Error(codes.AlreadyExists, "dup")), http.StatusConflict, 6, "call failed: rpc error: code = AlreadyExists desc = dup"},
		{"plain error", errors.New("boom"), http.StatusInternalServerError, 2, "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			httpStatus, grpcCode, message := HTTPStatus(tc.err)
			if httpStatus != tc.httpStatus || grpcCode != tc.grpcCode || message != tc.message {
				t.Fatalf("HTTPStatus() = %d, %d, %q; want %d, %d, %q",
					httpStatus, grpcCode, message, tc.httpStatus, tc.grpcCode, tc.message)
			}
		})
	}
}
