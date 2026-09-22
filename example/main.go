package main

import (
	"context"
	"log"
	"net"

	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	userv1 "github.com/Linuxea/protoc-gen-gin/example/proto/user/v1"

	_ "github.com/Linuxea/protoc-gen-gin/example/docs"
)

// @title           protoc-gen-gin Example API
// @version         1.0
// @description     HTTP API adapted from gRPC services by protoc-gen-gin.
func main() {
	conn := serveGRPC("127.0.0.1:9091", func(s *grpc.Server) {
		userv1.RegisterUserServiceServer(s, &userService{users: map[int64]*userv1.GetUserResponse{
			1: {Id: 1, Name: "alice", Email: "alice@example.com"},
		}})
		userv1.RegisterNotificationServiceServer(s, notificationService{})
	})

	router := gin.Default()
	router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	userv1.RegisterUserServiceGin(router, userv1.NewUserServiceClient(conn))
	userv1.RegisterNotificationServiceGin(router, userv1.NewNotificationServiceClient(conn))

	log.Println("grpc on :9091, http on :8080, swagger ui at http://localhost:8080/swagger/index.html")
	if err := router.Run(":8080"); err != nil {
		log.Fatalf("http server: %v", err)
	}
}

func serveGRPC(addr string, register func(*grpc.Server)) *grpc.ClientConn {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("grpc listen %s: %v", addr, err)
	}
	grpcSrv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		log.Printf("grpc <- %s", info.FullMethod)
		return handler(ctx, req)
	}))
	register(grpcSrv)
	go func() { _ = grpcSrv.Serve(lis) }()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("grpc client %s: %v", addr, err)
	}
	return conn
}
