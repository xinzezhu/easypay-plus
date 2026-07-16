# EasyPay Plus

使用一个易支付商户为多个自有产品提供统一下单、回调验签和支付结果分发。后端使用 Go + MySQL，管理台由 Go 服务直接托管。

## 已实现

- 产品管理：独立 `App ID`、API 密钥、通知密钥和回调地址
- 产品订单 API：HMAC-SHA256 鉴权、时间窗口和 nonce 防重放
- 易支付下单：全局唯一 `payId`，使用中转订单 ID 作为 `param`
- 易支付通知：兼容文档中的 `orderId` / `payId` 两种验签字段
- 支付幂等：订单状态更新与产品通知事件在同一个 MySQL 事务中提交
- 产品通知：独立签名，要求返回 `success`，失败按 1/5/10/30/60/180/360 分钟重试
- 管理台：产品启停、订单筛选、通知状态和人工重试
- 模拟通道：没有真实商户配置时可走通下单和回调流程

## 本地预览

本机没有 MySQL 时可以使用不持久化的内存模式：

```powershell
$env:DB_DRIVER="memory"
$env:EPAY_MOCK="true"
go run ./cmd/server
```

打开 `http://localhost:8080`，开发环境默认管理令牌为 `dev-admin-token`。

## MySQL 运行

要求 Go 1.26+、MySQL 8.0+。先创建数据库和最小权限账号：

```sql
CREATE DATABASE easypay_plus CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER 'easypay'@'%' IDENTIFIED BY 'replace-with-a-strong-password';
GRANT SELECT, INSERT, UPDATE, DELETE, CREATE, ALTER, INDEX, REFERENCES
ON easypay_plus.* TO 'easypay'@'%';
```

复制 `.env.example` 为 `.env`，至少修改：

```dotenv
ADMIN_TOKEN=随机管理令牌
APP_MASTER_KEY=至少32位随机主密钥
MYSQL_DSN=easypay:数据库密码@tcp(127.0.0.1:3306)/easypay_plus?parseTime=true&charset=utf8mb4&loc=Local&multiStatements=true
```

然后运行：

```powershell
go run ./cmd/server
```

也可以使用 `compose.yaml` 启动应用与 MySQL。生产部署前必须替换 Compose 中的默认密码，并提供公网 HTTPS `PUBLIC_BASE_URL`。
容器端口默认只绑定到 `127.0.0.1`，可通过 `APP_PORT` 指定反向代理使用的本机端口。
挂载到域名子路径时，同时设置 `BASE_PATH`（例如 `/easypay`）并让反向代理去掉该前缀。

## 切换真实易支付

```dotenv
EPAY_MOCK=false
EPAY_BASE_URL=https://epay.jylt.cc
EPAY_MCH_ID=商户ID
EPAY_SECRET=通讯密钥
EPAY_CALLBACK_SIGN_MODE=auto
PUBLIC_BASE_URL=https://pay.example.com
```

易支付异步通知地址是：

```text
https://pay.example.com/api/epay/notify
```

首次接入建议保持 `EPAY_CALLBACK_SIGN_MODE=auto`，完成一笔小额实测并确认日志中的 `signField` 后，再固定为 `orderId` 或 `payId`。

## 产品下单协议

产品请求：

```http
POST /api/v1/orders
Content-Type: application/json
X-App-Id: prod_xxx
X-Timestamp: 1784131200
X-Nonce: 至少16位随机字符串
X-Signature: hex(hmac_sha256(API_SECRET, timestamp + "." + nonce + "." + 原始请求体))

{"productOrderNo":"MEMBER-1001","amount":"9.90","payType":2,"goodsName":"月度会员"}
```

金额必须使用字符串，支持最多两位小数。相同产品的 `productOrderNo` 重复提交相同内容时返回原订单；内容不同则拒绝。

查询订单使用相同请求头签名，原始请求体为空：

```http
GET /api/v1/orders/MEMBER-1001
```

## 产品支付通知

中转服务向产品配置的 `notifyUrl` 发送 JSON，并携带：

```http
X-Easypay-Event-Id: evt_xxx
X-Easypay-Timestamp: 1784131200
X-Easypay-Signature: hex(hmac_sha256(NOTIFY_SECRET, timestamp + "." + eventId + "." + 原始请求体))
```

产品完成幂等处理后必须返回 HTTP 2xx 和纯文本 `success`。事件 ID 与产品订单号都可以作为去重依据。

## 验证

```powershell
go test ./...
go vet ./...
```

