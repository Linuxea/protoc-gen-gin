package main

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	userv1 "github.com/Linuxea/protoc-gen-gin/example/proto/user/v1"
)

type userService struct {
	userv1.UnimplementedUserServiceServer
	users map[int64]*userv1.GetUserResponse
}

func (s *userService) GetUser(_ context.Context, req *userv1.GetUserRequest) (*userv1.GetUserResponse, error) {
	u, ok := s.users[req.Id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "user %d not found", req.Id)
	}
	return u, nil
}

func (s *userService) UpdateUser(_ context.Context, req *userv1.UpdateUserRequest) (*userv1.UpdateUserResponse, error) {
	if req.Id == 0 {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	return &userv1.UpdateUserResponse{Ok: true}, nil
}

type notificationService struct {
	userv1.UnimplementedNotificationServiceServer
}

func (notificationService) Send(_ context.Context, req *userv1.SendRequest) (*userv1.SendResponse, error) {
	return &userv1.SendResponse{MessageId: "msg-1"}, nil
}
