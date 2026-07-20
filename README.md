# EasyPay Plus

用一个易支付商户为多个自有产品提供统一下单、验签和支付结果分发。服务端使用 Go + MySQL，管理台由服务本身托管。

示例管理台地址：`https://pay.example.com`

## 产品接入方式

产品不需要调用“创建产品”的后台接口，也不需要直接对接易支付。

1. 登录管理台，在“产品管理”中点击“添加产品”。
2. 填写产品名称、产品代码、该产品自己的异步通知地址；支付完成返回地址可选。
3. 创建成功后，保存弹窗中仅显示一次的 `App ID`、`API Secret` 和 `Notify Secret`。
4. 将这三个值配置到该产品的**服务端**，按 [产品接入协议](docs/product-integration.md) 创建订单、接收支付通知。

每个产品有独立凭据和通知地址。前端页面或移动端不得保存 `API Secret`、`Notify Secret`，它们只能放在业务产品的服务端环境变量或密钥管理服务中。

## 易支付商户配置

在易支付商户后台填写以下地址：

| 配置项 | 地址 |
| --- | --- |
| 异步回调 | `https://pay.example.com/api/epay/notify` |
| 同步回调 | `https://pay.example.com/payment/return` |
| 超时回调 | `https://pay.example.com/payment/timeout` |

中转服务在每次创建易支付订单时也会传入相同的回调地址。支付成功以产品的异步通知验签结果为准，浏览器同步跳转不能作为发货依据。

当 `EPAY_MOCK=false` 且使用微信/支付宝原生收款码模式时，易支付还依赖商户侧的监听设备或监控端在线上报支付结果。通常需要一台已登录对应收款账号的手机或挂机设备保持在线；如果监听设备离线、被系统杀后台、网络异常，用户即使扫码或完成支付，订单也可能长时间停留在 `pending`，最终按超时变为 `expired`。

## 运行与配置

生产环境使用 MySQL，并在 `.env` 设置：

```dotenv
PUBLIC_BASE_URL=https://pay.example.com
ADMIN_TOKEN=随机且足够长的管理令牌
APP_MASTER_KEY=至少32位随机主密钥
MYSQL_DSN=easypay:数据库密码@tcp(mysql:3306)/easypay_plus?parseTime=true&charset=utf8mb4&loc=Local&multiStatements=true

EPAY_MOCK=false
EPAY_BASE_URL=https://epay.jylt.cc
EPAY_MCH_ID=易支付商户ID
EPAY_SECRET=易支付通讯密钥
EPAY_CALLBACK_SIGN_MODE=auto
```

`EPAY_CALLBACK_SIGN_MODE=auto` 同时兼容易支付文档中 `orderId` / `payId` 的回调验签差异。完成一笔小额实测并在应用日志确认验签字段后，可固定为 `orderId` 或 `payId`。

`compose.yaml` 默认只把应用绑定到 `127.0.0.1`，应通过 Caddy、Nginx 等反向代理提供 HTTPS。部署在域名子路径时可设置 `BASE_PATH`，例如 `/easypay`。

本地演示可使用内存和模拟通道：

```powershell
$env:DB_DRIVER="memory"
$env:EPAY_MOCK="true"
go run ./cmd/server
```

打开 `http://localhost:8080`，开发环境默认管理令牌为 `dev-admin-token`。

## 验证

```powershell
go test ./...
go vet ./...
```

