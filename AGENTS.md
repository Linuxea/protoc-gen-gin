# AGENTS.md

protoc 插件：为 gRPC 一元方法生成 gin HTTP 适配层（同名 handler + swag 注解）。

## 两个独立 Go 模块

- 根模块 `github.com/Linuxea/protoc-gen-gin`：插件本体 + `ginruntime/`（生成代码的运行时依赖包）。命令在根目录跑。
- `example/`：独立子模块（go.mod 有 `replace github.com/Linuxea/protoc-gen-gin => ../`），使用新 grpc API，需 Go >= 1.24。命令要 `cd example` 后单独跑。

```bash
go build ./... && go vet ./... && go test ./...        # 根模块
cd example && go test ./...                             # example（e2e + swag 断言）
cd example && go test -short ./...                      # 跳过 swag 重生成（快）
go test ./internal -run TestGenerateFileContent         # 单测生成逻辑
```

## 硬约束（改坏会静默坑用户）

- **根模块 go 指令必须保持 `go 1.19`**，依赖钉在 grpc v1.58.3 / protobuf v1.33.0——为了让老工具链能 `go install`。注意：用新版本地 Go 跑 `go mod tidy` 可能把指令改写成三段式（如 `1.25.0`），导致老 Go 安装失败（已实际发生过）。改动依赖后必须验证：
  ```bash
  GOTOOLCHAIN=go1.19.13 go build ./... && GOTOOLCHAIN=go1.19.13 go test ./...
  ```
  （example 模块不受此限制，当前 `go 1.25.0`。）
- **`ginruntime` 包不可改名回 `runtime`**：swag 解析注解时别名与标准库 runtime 冲突，swagger.json 里错误 schema 会静默变空（无 $ref）。
- **handler 必须保持为顶层具名函数**：swag 只解析包级函数声明的 godoc，改成 Register 内闭包会使注解全部失效。
- `example/go.mod` 必须显式 `require github.com/swaggo/swag v1.16.6`：gin-swagger 会解析到老 swag，而 `swag init` 生成的 docs.go 用了新 Spec 字段，缺这行会编译失败。
- 发布只走 tag（`git tag vX.Y.Z && git push origin vX.Y.Z`），并同步更新 `internal/generator.go` 里的 `Version` 常量。无 tag 仓库的 `@latest` 有代理负缓存问题（已实际发生过）。

## 重新生成 example（改了 internal/generator.go 之后）

生成产物（`*.pb.go`、`example/docs/`）全部入库，改插件后必须同步再生成：

```bash
go install .                      # 1. 先重建插件二进制
cd example/proto                  # 2. 必须在 proto 目录里跑（在别处跑会把产物写歪位置）
PATH="$HOME/go/bin:$HOME/.local/protoc/bin:$PATH" protoc -I . \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  --gin_out=. --gin_opt=paths=source_relative \
  user/v1/user.proto
cd .. && go run github.com/swaggo/swag/cmd/swag@v1.16.6 init --parseDependency --output docs
go test ./...                     # e2e 会校验生成物行为；TestSwaggerDocs 会重跑 swag init
```

环境依赖：protoc（本机装在 `~/.local/protoc/bin`）、`~/go/bin` 下的 protoc-gen-go / protoc-gen-go-grpc / protoc-gen-gin。

## 已知副作用

- `go test ./...`（example，非 short）会重新生成 `example/docs/`，跑完 git 可能出现 docs 的 diff，属预期。
- 单测断言生成内容时不要写死注解对齐空格（gofmt 会重排注解块），断言用 `"@Router"` + 路径串分开匹配。

## 设计要点速查

- 生成物：每方法 `<Service><Method>Handler(cli XxxClient) gin.HandlerFunc`（带完整 swag 注解，@Description 取 proto leading comment）+ `Register<Service>Gin(r gin.IRoutes, cli)`；路由 `POST /<proto包名>.<Service>/<Method>`；流式方法跳过并告警（stderr + 生成物内 WARNING 注释）。
- `ginruntime`：`Error` 错误体（`{"code":<grpc码>,"message":...}`）+ `HTTPStatus()` gRPC→HTTP 映射（grpc-gateway 同款）。独立成包是为避免同包多生成文件的类型重复定义。
