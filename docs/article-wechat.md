<!--
公众号排版提示（发布前删除）：
1. 公众号编辑器不吃原生 Markdown，建议用 mdnice / Doocs md / 135 编辑器转格式后粘贴；
2. 代码块在手机上横向滚动很劝退，正文里的代码已尽量压短，超长片段建议换成截图；
3. 文中 "配图建议" 注释处补截图效果更好；
4. 外链无法直接点击，仓库地址已用纯文本，可在"阅读原文"放链接。
-->

# 不写一行胶水代码：我把 gRPC 服务直接变成了 HTTP 接口

> 备选标题：
> - 前端要 HTTP、合作方只会 curl：我用一个 protoc 插件解决了
> - proto 一个字不改，gRPC 服务也能有 Swagger

前端同学调我们的服务，可能根本不知道背后是 gRPC：发一个 HTTP 请求，JSON 就回来了，合作方拿 curl 也能直接调通。这一切存在了太久，久到没人觉得奇怪——至于一个 HTTP 请求到底是怎么落到 gRPC 方法上的，我也从来没细想过。

直到前阵子有朋友问我：它到底是怎么实现的？我第一反应是「中间肯定有一层东西在做转换」，再往下就答不上来了。这个「答不上来」让我有点在意。正好之前了解过 protoc 的插件系统，一直想找机会自己动手写一个，索性把两件事合在一起：实践一遍，顺便把过程记下来。

动手之前，我给自己定了一条规矩：proto 一个字不改。HTTP 映射必须是生成出来的、能推导的，而不是往 proto 里手写注解——同一份事实写两遍，迟早会对不上。

当然，会认真琢磨这件事，还跟我们项目里的一个现状有关。它才是我下决心动手的起点。

## 一、先说一个我们项目里的现状

我们项目里有个延续至今的设计：每个服务对外只暴露一个 gRPC 方法。比如 user 服务只有一个 `Call`，具体要办什么事，藏在请求体的 `data` 字段里——查用户、改资料、发通知，走的都是这一个入口。

这个设计有它明确的出发点，我完全能理解：网关、鉴权、限流只需要配置一次；加功能不用动 proto，也就没有评审、重新生成、客户端升级这一串流程；服务端实现也不用每次动接口，多一个分支就行。

这些好处都是真实存在过的。但在实际协作里，另一面同样真实：静态契约能带来的东西，基本都消失了。

举几个具体的。

先说类型检查和自动生成的客户端。细粒度方法下，调用长这样：

```go
resp, err := client.GetUser(ctx, &userv1.GetUserRequest{Id: 1})
```

请求和响应都是编译期信息，各语言的客户端从同一份 proto 生成，行为一致。单入口下呢，只有 `Call(ctx, &CallRequest{Action: ..., Data: payload})`，`payload` 是字符串或字节，业务结构只活在文档里，每个调用方自己拼、自己解，编译器帮不上忙。

再说监控。gRPC 的拦截器天然按 `grpc_method` 打点，每个方法的 QPS、P99、错误率、告警、限流都能单独配。单入口下，所有流量坍缩成一条 `grpc_method="Call"`，想看某个业务的 P99，要么解析 `data`——成本高，而且它可能是二进制，根本解不了；要么自己埋点。

最后是接口文档。细粒度下文档是生成的，和实现同源；单入口下 Swagger 里只有那一个入口，`data` 里能填什么，全靠手写，而手写的东西迟早过期。

说清楚一点：这些能力不是「绝对没有」，而是退到了事实标准之外。监控要自己埋、客户端要手写序列化，补出来的东西还会跟着 `data` 里的约定漂移；而在细粒度方法下，它们都是工具链免费附送的。

最直接的代价还是落在联调上：对接方拿到接口，不知道该往 `data` 里放什么；出了问题，也只能对着一句笼统的 message 猜。

后来我想明白了，这不是哪个设计错了，而是一个取舍：用灵活性换掉了契约的确定性。判断标准可以浓缩成一句话——一个操作如果是可枚举的「方法」，它就应该是 RPC 方法；只有不可枚举的，比如插件、工作流这类动态 action，才适合放进数据字段。

所以我不打算抱怨现状，也不想说服谁去动历史包袱。我更想做的是：让以后新写的每一个方法，都能低成本地拿到契约带来的好处。

于是就有了 protoc-gen-gin。这篇文章聊聊它怎么用、怎么实现的。

## 二、先交代思路：适配器模式、目标与取舍

在展示成品之前，先把几件事交代清楚：它是什么思路，要干什么，靠什么实现，这一版又不做什么。带着这些再看代码会顺很多。

先说设计模式。这个项目的内核，是设计模式里的适配器模式，英文叫 Adapter——把一个类的接口转换成客户端期望的另一种接口，让原本不兼容的两边能一起工作。生活里的类比是电源转换头：墙上的插座和你的笔记本插头都不用改，中间加一个转换头就行。

放到这里也一样。调用方期望的是「HTTP 请求进、JSON 响应出」，服务端提供的是「gRPC 方法、protobuf 消息」，两边在传输协议和数据格式上都不兼容。中间那个转换头，负责把 HTTP 请求翻译成对 gRPC 方法的调用，再把结果和错误翻译回来。

转换头有两种做法：一种是运行时做，在进程外加一个网关，请求到了再翻译；另一种是生成时做，直接把转换代码生成出来。这个项目选的是后者，至于为什么，后面见分晓。

目标也很简单：让 gRPC 服务以尽可能小的改动，多出一层 HTTP/JSON 接口，外加一份能自动更新的接口文档。也就是给每个一元方法，生成一个专属的转换头。

技术底座是四块现成的积木：

- protoc 插件系统。protoc 会把编译好的描述符 `CodeGeneratorRequest` 喂给插件，插件把要生成的代码 `CodeGeneratorResponse` 写回去。官方有个 `protogen` 包封装了协议细节，插件只需要关心「生成什么」；
- protobuf 描述符。服务、方法、消息、注释都在里面，这是插件的唯一输入——也正因为如此，proto 才不用为 HTTP 加任何注解；
- gin 和 gRPC Client。生成物本质就是普通的 gin handler，内部拿着一个 gRPC Client 转发调用，不额外引入运行时网关；
- swaggo 注解。生成 handler 的同时把 swag 注解一起产出，`swag init` 跑一下就有 Swagger。

这一版先不做的事情，其实也就是几个取舍：

- 流式方法先放着，只支持一元方法；
- 参数绑定只做 POST + JSON body，path/query 以后再说；
- 路由形状先固定为 `POST /<包名>.<Service>/<Method>`，自定义路径留给未来；
- 不做独立网关进程，先老老实实生成 Go 代码。

好，铺垫完了，看东西。

## 三、实现思路：一个 protoc 插件长什么样

protoc 插件的工作方式很朴素：protoc 把「要生成哪些文件、这些文件长什么样」序列化成 `CodeGeneratorRequest`，写到插件进程的 stdin；插件算完，把结果打包成 `CodeGeneratorResponse`，从 stdout 写回去。插件本身就是一个普通的可执行文件。

这里有个约定：可执行文件必须叫 `protoc-gen-gin`，这样 `--gin_out` 参数才能找到它。

官方有 `protogen` 包把协议细节封好了，插件主函数只有十几行：

```go
func main() {
	protogen.Options{ParamFunc: flag.CommandLine.Set}.Run(func(gen *protogen.Plugin) error {
		gen.SupportedFeatures = uint64(pluginpb.CodeGeneratorResponse_FEATURE_PROTO3_OPTIONAL)
		for _, f := range gen.Files {
			if !f.Generate {
				continue
			}
			internal.GenerateFile(gen, f)
		}
		return nil
	})
}
```

真正干活的是 `GenerateFile`：

- 遍历文件里的 service 和 method，只处理一元方法；
- 为每个方法生成一个顶层具名函数 `XxxHandler`，返回 `gin.HandlerFunc`。这里必须是包级具名函数，swag 只认包级函数上的注解；
- 再生成一个 `RegisterXxxGin`，把每个方法注册成 POST 路由；
- import 交给 `protogen.QualifiedGoIdent` 统一管理，输出自动过 gofmt。

生成物运行时只依赖一个小包 `ginruntime`，错误体结构和 gRPC 到 HTTP 的状态码映射都在里面。没有别的魔法。

## 四、它到底生成什么

先看效果。假设有这样一段 proto，注意方法上的注释，后面会用到：

```proto
service UserService {
  // GetUser 根据 ID 获取用户。
  rpc GetUser(GetUserRequest) returns (GetUserResponse);
}
```

插件先装上：

```bash
go install github.com/Linuxea/protoc-gen-gin@latest
```

配套的命令如下：

```bash
protoc -I . \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  --gin_out=. --gin_opt=paths=source_relative \
  user/v1/user.proto
```

跑完多出一个 `user.gin.pb.go`，核心就是一个 handler：

```go
func UserServiceGetUserHandler(cli UserServiceClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req GetUserRequest
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			c.JSON(400, ginruntime.Error{Code: 3, Message: "invalid request: " + err.Error()})
			return
		}
		resp, err := cli.GetUser(c.Request.Context(), &req)
		if err != nil {
			httpStatus, code, message := ginruntime.HTTPStatus(err)
			c.JSON(httpStatus, ginruntime.Error{Code: code, Message: message})
			return
		}
		c.JSON(200, resp)
	}
}

func RegisterUserServiceGin(r gin.IRoutes, cli UserServiceClient) {
	r.POST("/user.v1.UserService/GetUser", UserServiceGetUserHandler(cli))
}
```

业务侧挂载三行搞定：

```go
conn, _ := grpc.NewClient("127.0.0.1:9091", grpc.WithTransportCredentials(insecure.NewCredentials()))
r := gin.Default()
userv1.RegisterUserServiceGin(r, userv1.NewUserServiceClient(conn))
```

另外，`UserServiceGetUserHandler` 上面还带着一整块 swag 注解：`@Summary`、`@Description`、`@Param`、`@Success`、`@Router`…… `swag init` 一跑，Swagger UI 直接能用，不用再单独维护文档。

<!-- 配图建议：Swagger UI 截图 -->

## 五、这个约束的边界：什么能动，什么不能动

回到上一节：从 proto 到生成物，从头到尾，proto 一个字都没动。

这不是巧合。protoc 交给插件的描述符里，包名、服务名、方法名、请求响应消息、注释都在，推导路由和文档需要的信息一样不少。信息本来就全，再往 proto 里手写一份 HTTP 映射，等于把同一份事实写两遍；写两遍就会漂移，最后文档和实现对不上。

还有更现实的一层：proto 是跨端共享的契约，Go 服务、Java 服务、前端生成的类型都从它来。改一次契约，意味着评审、重新生成、各端升级一整条链路。而 HTTP 怎么暴露，是传输层的事，不该写回契约里。

所以这个约束准确的说法是：proto 作为接口契约的部分不动，其他都能动。

不能动的只有两样。一是 `.proto` 文件本身，不写 `google.api.http`，不加自定义 option，插件也不读这些；二是路由形状，目前固定为 `POST /<包名>.<Service>/<Method>`，路径和动词不能定制。这是零改动的直接代价——想改路径，得先设计「从注释里读配置」这类机制，还在计划里。

其余都能动。服务和方法是正常演进的，加一个 RPC 方法，就自动多一条路由和一份文档；方法上的注释会变成 Swagger 的 `@Description`，算文档输入，不算契约；挂载方式由你决定，`gin.IRoutes` 支持挂到任意引擎，或者 `Group("/api")` 下面；生成物之外的一切，比如 Client 地址、中间件、鉴权、限流，全在业务侧自己控制。

一句话：proto 管「有哪些方法」，插件管「方法怎么变成 HTTP」，业务侧管「把它挂到哪儿」。

## 小结

protoc 插件是个投入产出比很高的方向：生成逻辑不到 200 行，一次写好，所有服务复用，而且 proto 零侵入。

仓库：`github.com/Linuxea/protoc-gen-gin`，GitHub 搜 protoc-gen-gin 也能找到，欢迎 star / issue。

如果你也在纠结「gRPC 服务怎么优雅地暴露 HTTP」，不妨试试这个思路。觉得有用的话，点个赞和在看，也欢迎转发给正在写胶水代码的同事。

我们下篇见。
