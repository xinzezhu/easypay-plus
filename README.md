# EasyPay Plus

EasyPay Plus 是一个易支付中转服务。

它的核心用途是：用同一个易支付商户，同时服务多个自己的业务产品，而且每个产品仍然拥有独立的接入凭据、回调地址和订单号空间。产品方只对接 EasyPay Plus，不直接对接易支付。

示例管理台地址：`https://pay.example.com`

## 这个项目解决什么问题

如果你有多个站点、App、小程序或 SaaS 产品，直接共用一个易支付商户，通常会遇到这些问题：

- 不好区分订单到底属于哪个产品。
- 每个产品都要自己处理易支付下单、回调验签和状态查询。
- 一个产品的回调异常，可能影响其他产品。
- 想做统一收银台、统一通知重试、统一订单管理时，代码会越来越乱。

EasyPay Plus 把这层公共能力抽出来：

1. 你的产品服务端调用 EasyPay Plus 创建订单。
2. EasyPay Plus 再去调用易支付创建真实订单。
3. 用户打开 EasyPay Plus 返回的收银台地址完成支付。
4. EasyPay Plus 验证易支付回调。
5. EasyPay Plus 再把支付成功结果通知到各产品自己的 `notifyUrl`。

## 它能做什么

- 用一个易支付商户接多个自有产品。
- 管理台创建产品并生成独立的 `App ID`、`API Secret`、`Notify Secret`。
- 产品服务端通过签名 API 创建订单、查询订单。
- 自动提供中转收银台，展示商品名、金额、二维码和倒计时。
- 验证易支付回调后，再向下游产品分发支付成功通知。
- 下游通知失败自动重试。
- 支持本地模拟模式，不接真实商户也能先联调。
- 支持 Go 二进制直接运行。
- 支持 Docker / Docker Compose 部署。

## 适合什么场景

- 你有多个业务产品，但只想维护一个易支付商户。
- 你想给每个产品分配独立密钥，而不是全站共用一套商户配置。
- 你希望把订单创建、收银台、回调验签、通知重试做成统一基础设施。

## 产品怎么接入

产品不需要调用“创建产品”的后台接口，也不需要直接对接易支付。

1. 登录管理台，在“产品管理”里创建一个产品。
2. 填写产品名称、产品代码、该产品自己的 `notifyUrl`，`returnUrl` 可选。
3. 保存创建成功弹窗里显示一次的 `App ID`、`API Secret`、`Notify Secret`。
4. 把这三个值配置到该产品自己的服务端。
5. 产品服务端按 [单个产品接入协议](docs/product-integration.md) 调用 `/api/v1/orders` 创建订单。
6. 把返回的 `payUrl` 给前端，让用户跳转到 EasyPay Plus 的收银台。

每个产品有独立凭据和通知地址。前端页面、浏览器、小程序前端或 App 客户端都不能保存 `API Secret` 和 `Notify Secret`，它们只能放在业务产品自己的服务端。

## 5 分钟快速部署

推荐直接用 Docker Compose。

### 1. 复制环境变量文件

```powershell
Copy-Item .env.example .env
```

### 2. 修改最关键的配置

至少把 `.env` 里的这些值改掉：

```dotenv
PUBLIC_BASE_URL=https://pay.example.com
APP_BIND_IP=127.0.0.1
APP_PORT=8080

ADMIN_TOKEN=换成一个长随机串
APP_MASTER_KEY=换成至少32位随机串

MYSQL_ROOT_PASSWORD=换成数据库 root 密码
MYSQL_PASSWORD=换成应用数据库密码

EPAY_MOCK=false
EPAY_MCH_ID=你的易支付商户ID
EPAY_SECRET=你的易支付通讯密钥
EPAY_CALLBACK_SIGN_MODE=auto
```

如果你只是先本地体验，不接真实商户，可以先保留：

```dotenv
EPAY_MOCK=true
PUBLIC_BASE_URL=http://localhost:8080
APP_BIND_IP=127.0.0.1
APP_PORT=8080
```

如果你想直接通过服务器 IP 访问，而不是先上反向代理，可以这样配：

```dotenv
PUBLIC_BASE_URL=http://你的服务器IP:8080
APP_BIND_IP=0.0.0.0
APP_PORT=8080
```

### 3. 启动服务

```powershell
docker compose up -d --build
```

查看状态：

```powershell
docker compose ps
```

健康检查：

```text
http://localhost:8080/api/health
```

### 4. 打开管理台

启动成功后，打开：

- 本地调试：`http://localhost:8080`
- 服务器直连：`http://你的服务器IP:8080`
- 域名部署：`https://你的域名`

然后用 `.env` 里的 `ADMIN_TOKEN` 登录管理台，创建第一个产品。

## 支持 Docker 吗

支持，仓库已经自带：

- `Dockerfile`
- `compose.yaml`

`compose.yaml` 会启动两个服务：

- `mysql`：MySQL 8.4
- `app`：EasyPay Plus 应用本身

默认情况下，`compose.yaml` 只把应用绑定到 `127.0.0.1:8080`，更适合放在 Caddy、Nginx 等反向代理后面提供 HTTPS。如果你暂时还没有域名或代理，把 `APP_BIND_IP` 改成 `0.0.0.0` 就能直接对外监听。

## 易支付商户后台怎么填

在易支付商户后台填写以下地址：

| 配置项 | 地址 |
| --- | --- |
| 异步回调 | `https://pay.example.com/api/epay/notify` |
| 同步回调 | `https://pay.example.com/payment/return` |
| 超时回调 | `https://pay.example.com/payment/timeout` |

EasyPay Plus 每次创建易支付订单时，也会把相同的地址传给易支付。支付成功应以下游产品收到并验签通过的异步通知为准，不能以浏览器跳转结果作为发货依据。

## 一个重要前提

当 `EPAY_MOCK=false` 且使用微信/支付宝原生收款码模式时，易支付通常还依赖商户侧的监听设备或监控端在线上报支付结果。也就是说，通常需要一台已登录对应收款账号的手机或挂机设备保持在线。

如果监听设备离线、被系统杀后台、网络异常，用户即使扫码或完成支付，订单也可能长时间停留在 `pending`，最后按超时变成 `expired`。

## 不用 Docker 的启动方式

如果你想直接跑 Go 程序，也可以：

```powershell
go run ./cmd/server
```

或者先编译：

```powershell
go build -o easypay-plus.exe ./cmd/server
.\easypay-plus.exe
```

如果只是本地演示，可直接用内存存储和模拟通道：

```powershell
$env:DB_DRIVER="memory"
$env:EPAY_MOCK="true"
go run ./cmd/server
```

开发环境默认管理令牌是 `dev-admin-token`。

## 更多文档

- [单个产品接入协议](docs/product-integration.md)
- [环境变量示例](.env.example)

## 验证

```powershell
go test ./...
go vet ./...
```
