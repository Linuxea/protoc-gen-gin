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

插件本体兼容 Go >= 1.19（`go install` 侧）。生成的代码依赖 `github.com/Linuxea/protoc-gen-gin/ginruntime` 运行时包（错误体类型 + 状态码映射），业务模块需要依赖本模块；example 子模块因使用较新的 grpc API 需要 Go >= 1.24 构建，仅供参考。

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

## 运行期适配（无代码生成）：`httpadapter`

不想跑 protoc 插件、或者要给**没有源码/pb 包**的 gRPC 服务挂 HTTP 时，用 `httpadapter`：运行期读取 proto 描述符，用 `conn.Invoke` 按方法全名转发。路由（`POST /<包名>.<Service>/<Method>`）、错误体、状态码映射、空 body、流式跳过都与生成代码一致。它是纯 `http.Handler`，不依赖 gin，根模块仍保持 Go 1.19 可用。

**模式 A：本地描述符**（已 import `*.pb.go`，描述符来自 `protoregistry.GlobalFiles`，消息用生成的 Go 类型）

```go
a := httpadapter.New(conn)
skipped, err := a.RegisterService("user.v1.UserService", "user.v1.NotificationService")
for _, rt := range a.Routes() {
	r.POST(rt.Path, gin.WrapH(rt.Handler)) // 或整体挂载：r.POST("/*rpc", gin.WrapH(a))
}
```

**模式 B：服务端反射**（零生成代码，目标服务需 `reflection.Register(s)`，消息走 `dynamicpb`）

```go
files, services, err := httpadapter.FetchFiles(ctx, conn)
a := httpadapter.New(conn, httpadapter.WithFiles(files))
_, err = a.RegisterService(services...)
```

可选项：`WithPrefix("/api")`、`WithMetadata(func(*http.Request) metadata.MD)`（转发鉴权头等）、`WithMaxBodyBytes`（默认 4 MiB，超限 413）、`WithCallOptions`、`WithMarshalOptions` / `WithUnmarshalOptions`。

与生成代码的差异（JSON 编解码走 protojson 而非 encoding/json）：

| | 生成代码 | httpadapter |
|---|---|---|
| int64/uint64 输出 | 数字 `1` | 字符串 `"1"`（protojson 标准） |
| 枚举输出 | 数字 | 枚举名（如 `"SERVING"`） |
| 请求字段名 | proto 名（snake_case） | proto 名与 lowerCamel 均可 |
| 零值字段 | omitempty 省略 | 省略（可 `EmitUnpopulated`） |
| swag 文档 | 有 | 无（无具名 handler 可供 swag 解析） |
| 非 POST | 404（gin 默认） | 405 |

选择建议：对外 API、需要 swagger 文档 → 用插件生成；内部网关、调试入口、服务众多或拿不到 pb 包 → 用 `httpadapter`。

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
go test ./...          # 插件单测 + httpadapter（bufconn + health/reflection 服务）
cd example && go test ./...   # e2e（bufconn gRPC + httptest）+ swag 解析断言
```

example 是独立子模块（`replace protoc-gen-gin => ../`），含示例 proto、生成产物与可运行 demo（`go run .` 后访问 http://localhost:8080/swagger/index.html）。
