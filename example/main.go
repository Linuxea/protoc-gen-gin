package main

import (
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
	lis, err := net.Listen("tcp", "127.0.0.1:9091")
	if err != nil {
		log.Fatalf("grpc listen: %v", err)
	}
	grpcSrv := grpc.NewServer()
	userv1.RegisterUserServiceServer(grpcSrv, &userService{users: map[int64]*userv1.GetUserResponse{
		1: {Id: 1, Name: "alice", Email: "alice@example.com"},
	}})
	userv1.RegisterNotificationServiceServer(grpcSrv, notificationService{})
	go func() { _ = grpcSrv.Serve(lis) }()

	conn, err := grpc.NewClient("127.0.0.1:9091", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("grpc client: %v", err)
	}

	r := gin.Default()
	r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	userv1.RegisterUserServiceGin(r, userv1.NewUserServiceClient(conn))
	userv1.RegisterNotificationServiceGin(r, userv1.NewNotificationServiceClient(conn))

	log.Println("http server on :8080, swagger ui at http://localhost:8080/swagger/index.html")
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("http server: %v", err)
	}
}
