# 单个产品接入协议

生产服务地址：`https://pay.example.com`

这份协议供一个业务产品的服务端使用。业务产品的网页、App 或小程序先请求自己的服务端，再由业务服务端调用中转服务；不得从浏览器直接调用，也不得把任何密钥下发给客户端。

## 1. 一次性准备

在管理台 `https://pay.example.com` 创建产品，填入：

- 产品名称和产品代码。
- `notifyUrl`：该业务产品的支付结果通知地址，必须是公网可访问的完整 HTTP(S) URL。
- `returnUrl`：用户支付结束后的浏览器返回地址，可选。

创建成功后保存以下凭据。密钥只在创建成功弹窗中显示一次：

| 凭据 | 用途 |
| --- | --- |
| `App ID` | 产品身份，例如 `prod_xxx` |
| `API Secret` | 业务服务端调用下单、查单接口时签名 |
| `Notify Secret` | 业务服务端验证支付结果通知签名 |

这不是后台 API 创建流程；产品只需在管理台创建一次即可。

## 2. 请求签名

所有产品 API 请求都带以下请求头：

```text
X-App-Id: <App ID>
X-Timestamp: <Unix 秒级时间戳>
X-Nonce: <16-100 位随机字符串，仅允许 A-Z、a-z、0-9、_、->
X-Signature: <小写十六进制 HMAC-SHA256>
```

签名原文为：

```text
timestamp + "." + nonce + "." + rawBody
```

其中 `rawBody` 是实际发送的原始 UTF-8 请求体。不要对 JSON 重新格式化、排序或解析后再签名。接口允许前后 5 分钟的时间误差，同一个 `nonce` 只能使用一次。

Node.js 服务端签名示例：

```js
import crypto from "node:crypto";

function signedHeaders(appId, apiSecret, rawBody) {
  const timestamp = String(Math.floor(Date.now() / 1000));
  const nonce = crypto.randomBytes(24).toString("base64url");
  const source = `${timestamp}.${nonce}.${rawBody}`;
  const signature = crypto.createHmac("sha256", apiSecret).update(source, "utf8").digest("hex");
  return {
    "Content-Type": "application/json",
    "X-App-Id": appId,
    "X-Timestamp": timestamp,
    "X-Nonce": nonce,
    "X-Signature": signature,
  };
}
```

## 3. 创建支付订单

```http
POST https://pay.example.com/api/v1/orders
Content-Type: application/json
X-App-Id: <App ID>
X-Timestamp: <timestamp>
X-Nonce: <nonce>
X-Signature: <signature>
```

请求体：

```json
{
  "productOrderNo": "MEMBER-1001",
  "amount": "9.90",
  "payType": 2,
  "goodsName": "月度会员"
}
```

字段规则：

| 字段 | 规则 |
| --- | --- |
| `productOrderNo` | 产品内唯一订单号，1-100 位，首字符为字母或数字，后续只能使用字母、数字、`_`、`.`、`:`、`-`；同一产品重复提交相同内容会返回原订单。 |
| `amount` | 字符串金额，必须大于 0，最多 9 位整数和 2 位小数，例如 `"9.90"`。不要传浮点数。 |
| `payType` | `1` 为微信，`2` 为支付宝。 |
| `goodsName` | 1-50 个字符。 |

业务服务端应先生成固定的订单号和 JSON 字符串，再按该原始 JSON 签名并发送。示例：

```js
const body = JSON.stringify({
  productOrderNo: "MEMBER-1001",
  amount: "9.90",
  payType: 2,
  goodsName: "月度会员",
});

const response = await fetch("https://pay.example.com/api/v1/orders", {
  method: "POST",
  headers: signedHeaders(process.env.EASYPAY_APP_ID, process.env.EASYPAY_API_SECRET, body),
  body,
});
const result = await response.json();
```

成功时首次创建返回 HTTP `201`；相同订单号且内容完全一致的重试返回 HTTP `200` 和 `idempotent: true`。典型响应：

```json
{
  "order": {
    "id": "ord_xxx",
    "productOrderNo": "MEMBER-1001",
    "payId": "...",
    "payType": 2,
    "goodsName": "月度会员",
    "amount": "9.90",
    "reallyAmount": "9.90",
    "status": "pending",
    "payUrl": "https://...",
    "expiresAt": "2026-07-16T12:00:00Z"
  },
  "idempotent": false
}
```

将 `order.payUrl` 返回给业务前端，让用户跳转到本服务的收银台。收银台会展示商品、产品、应付金额、支付方式、二维码和剩余支付时间；二维码内容不会再直接暴露给业务前端。创建订单成功只代表已获得支付链接，不能据此开通权益或发货。

易支付商户设置的 5 分钟超时会以接口返回的 `expiresAt` 为准。倒计时结束后，收银台会隐藏二维码，中转服务会将待支付订单标记为 `expired`，此时应使用新的业务订单号重新创建订单。

常见失败响应为：

```json
{
  "error": {
    "code": "unauthorized",
    "message": "请求签名无效"
  }
}
```

`401` 表示身份、时间戳、nonce 或签名错误；`422` 表示订单内容不合法、产品已停用或易支付拒绝订单；`500` 可使用相同订单号重试。

## 4. 查询订单

如需在用户返回页面主动查询状态，使用相同签名规则，`rawBody` 必须为空字符串：

```http
GET https://pay.example.com/api/v1/orders/MEMBER-1001
X-App-Id: <App ID>
X-Timestamp: <timestamp>
X-Nonce: <nonce>
X-Signature: hex(hmac_sha256(API_SECRET, timestamp + "." + nonce + "."))
```

返回结构为：

```json
{
  "order": {
    "id": "ord_xxx",
    "productOrderNo": "MEMBER-1001",
    "status": "pending"
  }
}
```

订单状态可为 `creating`、`pending`、`paid`、`expired` 或 `failed`。浏览器返回或查单仅用于展示；支付成功仍应以第 5 节的签名通知为准。

## 5. 支付成功通知

易支付通知中转服务后，中转服务会向产品在管理台配置的 `notifyUrl` 发送：

```http
POST <notifyUrl>
Content-Type: application/json
X-Easypay-Event-Id: <evt_xxx>
X-Easypay-Timestamp: <Unix 秒级时间戳>
X-Easypay-Signature: <小写十六进制 HMAC-SHA256>
```

请求体：

```json
{
  "id": "evt_xxx",
  "type": "payment.succeeded",
  "createdAt": "2026-07-16T12:00:00Z",
  "relayOrderId": "ord_xxx",
  "productOrderNo": "MEMBER-1001",
  "payId": "...",
  "epayOrderId": "...",
  "payType": 2,
  "amount": "9.90",
  "reallyAmount": "9.90",
  "status": "paid"
}
```

验签原文为：

```text
timestamp + "." + eventId + "." + rawBody
```

使用 `Notify Secret` 计算 `HMAC-SHA256`，与 `X-Easypay-Signature` 做常量时间比较。仅在签名、时间戳、金额和订单归属都验证通过后，才执行发货、开通会员等业务操作。`X-Easypay-Event-Id` 和 `productOrderNo` 都应做幂等去重。

处理成功后必须返回 HTTP `2xx`，且响应体必须是纯文本：

```text
success
```

其他任何响应，包括返回 JSON、空响应或 HTTP 非 2xx，都会被视为投递失败。中转服务会在首次投递失败后按 1、5、10、30、60、180、360 分钟重试，最多投递 8 次；管理台可对失败通知手动重试。

## 6. 浏览器返回地址

当前收银台使用易支付的二维码模式（`isHtml=0`），不依赖易支付的同步跳转。若创建产品时填写了 `returnUrl`，收银台确认支付成功后会自动跳转到业务页面；超时或创建失败时保留手动返回入口，并带上：

```text
relayOrderId=<ord_xxx>
productOrderNo=<业务订单号>
status=paid 或 timeout
```

这个跳转只用于页面展示。业务前端应调用自己的服务端查询或等待异步通知，不能根据 URL 参数直接认定支付成功。

