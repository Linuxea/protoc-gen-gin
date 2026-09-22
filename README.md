# protoc-gen-gin

一个 protoc 插件：为 gRPC 服务的每个**一元（unary）方法**生成同名同参数的 gin HTTP 适配层，并同步生成 swaggo/swag 注解（`swag init` 可直接产出接口对接文档）。

## 特性

- 每个一元方法生成 `POST /<proto包名>.<Service>/<Method>` 路由，JSON 请求体绑定到对应的 request 消息
- handler 持有 gRPC `Client` 转发调用（grpc-gateway 风格），同一份代码适配本地/远端服务
- gRPC 状态码自动映射为 HTTP 状态码（NotFound→404、InvalidArgument→400、Unavailable→503 等），错误体统一为 `{"code": <gRPC码>, "message": "..."}`
- 每个方法一个具名 handler 函数，带完整 swag 注解（`@Summary`/`@Description`（取 proto 注释）/`@Tags`/`@ID`/`@Param`/`@Success`/`@Failure`/`@Router`）
- 流式（streaming）方法自动跳过，并在生成代码与 stderr 中警告
- 空请求体被容忍（视为零值 request），由服务端校验兜底

## 安装

```bash
go install github.com/Linuxea/protoc-gen-gin@latest   # 或本地: go install .
```

生成的代码依赖 `github.com/Linuxea/protoc-gen-gin/ginruntime` 运行时包（错误体类型 + 状态码映射），业务模块需要依赖本模块。

## 用法

```bash
cd proto
protoc -I . \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  --gin_out=. --gin_opt=paths=source_relative \
  user/v1/user.proto
```

每个 proto 生成 `xxx.gin.pb.go`，包含：

```go
// UserServiceGetUserHandler adapts the unary RPC user.v1.UserService.GetUser to a gin route.
//
// @Summary      GetUser
// @Description  GetUser 根据 ID 获取用户。
// @Tags         UserService
// @ID           user.v1.UserService.GetUser
// @Accept       json
// @Produce      json
// @Param        request body GetUserRequest true "GetUser request body"
// @Success      200 {object} GetUserResponse
// @Failure      400 {object} ginruntime.Error
// @Failure      500 {object} ginruntime.Error
// @Router       /user.v1.UserService/GetUser [post]
func UserServiceGetUserHandler(cli UserServiceClient) gin.HandlerFunc { ... }

func RegisterUserServiceGin(r gin.IRoutes, cli UserServiceClient) {
	r.POST("/user.v1.UserService/GetUser", UserServiceGetUserHandler(cli))
	// WARNING: streaming method WatchUsers is not registered (only unary methods are supported).
}
```

在业务侧挂载：

```go
conn, _ := grpc.NewClient("127.0.0.1:9090", grpc.WithTransportCredentials(insecure.NewCredentials()))
r := gin.Default()
userv1.RegisterUserServiceGin(r, userv1.NewUserServiceClient(conn))
```

`gin.IRoutes` 兼容 `*gin.Engine` 与 `router.Group(...)`。

## 接口文档（swaggo/swag）

插件只负责生成带注解的代码；文档在业务模块里生成：

```bash
swag init --parseDependency
```

随后用 gin-swagger 挂 UI：

```go
r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
```

注意：`swag init` 需要 `--parseDependency` 才能解析 `ginruntime.Error` 与 pb 结构体。

## gRPC → HTTP 状态码映射

| gRPC code | HTTP |
|---|---|
| InvalidArgument / FailedPrecondition / OutOfRange | 400 |
| Unauthenticated | 401 |
| PermissionDenied | 403 |
| NotFound | 404 |
| AlreadyExists / Aborted | 409 |
| ResourceExhausted | 429 |
| Canceled | 499 |
| Internal / Unknown / DataLoss | 500 |
| Unimplemented | 501 |
| Unavailable | 503 |
| DeadlineExceeded | 504 |

## 限制

- 流式方法（server/client/bidi stream）不生成路由
- 请求参数仅支持 JSON body（不做 path/query 参数绑定，保持与 gRPC 方法签名一致）
- pb 消息中的 oneof 字段在 swagger 中显示为空对象

## 开发

```bash
go test ./...          # 插件单测
cd example && go test ./...   # e2e（bufconn gRPC + httptest）+ swag 解析断言
```

example 是独立子模块（`replace protoc-gen-gin => ../`），含示例 proto、生成产物与可运行 demo（`go run .` 后访问 http://localhost:8080/swagger/index.html）。
