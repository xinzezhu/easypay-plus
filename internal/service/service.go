package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/easypay-plus/easypay-plus/internal/config"
	"github.com/easypay-plus/easypay-plus/internal/epay"
	"github.com/easypay-plus/easypay-plus/internal/model"
	"github.com/easypay-plus/easypay-plus/internal/money"
	"github.com/easypay-plus/easypay-plus/internal/secure"
	"github.com/easypay-plus/easypay-plus/internal/store"
)

var (
	codePattern    = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)
	orderNoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,99}$`)
	noncePattern   = regexp.MustCompile(`^[A-Za-z0-9_-]{16,100}$`)
)

type Gateway interface {
	CreateOrder(context.Context, epay.CreateRequest) (epay.CreateResponse, error)
	VerifyCallback(url.Values) (string, error)
	SignCallback(string, string, string, string, string, string) (string, string)
	MerchantID() string
}

type Service struct {
	store   store.Store
	gateway Gateway
	vault   *secure.Vault
	config  config.Config
	now     func() time.Time
}

type ProductCredentials struct {
	Product      model.Product `json:"product"`
	APISecret    string        `json:"apiSecret"`
	NotifySecret string        `json:"notifySecret"`
}

type CreateOrderInput struct {
	ProductOrderNo string `json:"productOrderNo"`
	Amount         string `json:"amount"`
	PayType        int    `json:"payType"`
	GoodsName      string `json:"goodsName"`
}

type OrderResult struct {
	Order      model.Order
	Idempotent bool
}

type PaymentEvent struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	CreatedAt      time.Time `json:"createdAt"`
	RelayOrderID   string    `json:"relayOrderId"`
	ProductOrderNo string    `json:"productOrderNo"`
	PayID          string    `json:"payId"`
	EpayOrderID    string    `json:"epayOrderId"`
	PayType        int       `json:"payType"`
	Amount         string    `json:"amount"`
	ReallyAmount   string    `json:"reallyAmount"`
	Status         string    `json:"status"`
}

func New(storage store.Store, gateway Gateway, vault *secure.Vault, cfg config.Config) *Service {
	return &Service{store: storage, gateway: gateway, vault: vault, config: cfg, now: time.Now}
}

func (s *Service) Store() store.Store { return s.store }

func (s *Service) ExpireDueOrders(ctx context.Context) (int64, error) {
	return s.store.ExpireDueOrders(ctx, s.now())
}

func (s *Service) CreateProduct(ctx context.Context, name, code, notifyURL, returnURL string) (ProductCredentials, error) {
	name, code = strings.TrimSpace(name), strings.TrimSpace(code)
	if name == "" || utf8.RuneCountInString(name) > 100 {
		return ProductCredentials{}, errors.New("产品名称不能为空且最多 100 个字符")
	}
	if !codePattern.MatchString(code) {
		return ProductCredentials{}, errors.New("产品代码须以小写字母开头，只能包含小写字母、数字、_、-")
	}
	if err := s.validateEndpoint(notifyURL, true); err != nil {
		return ProductCredentials{}, fmt.Errorf("通知地址无效: %w", err)
	}
	if returnURL != "" {
		if err := s.validateEndpoint(returnURL, false); err != nil {
			return ProductCredentials{}, fmt.Errorf("返回地址无效: %w", err)
		}
	}
	apiSecret, err := secure.RandomToken(32)
	if err != nil {
		return ProductCredentials{}, err
	}
	notifySecret, err := secure.RandomToken(32)
	if err != nil {
		return ProductCredentials{}, err
	}
	apiSecretEnc, err := s.vault.Encrypt(apiSecret)
	if err != nil {
		return ProductCredentials{}, err
	}
	notifySecretEnc, err := s.vault.Encrypt(notifySecret)
	if err != nil {
		return ProductCredentials{}, err
	}
	id, err := randomID("prod_")
	if err != nil {
		return ProductCredentials{}, err
	}
	now := s.now()
	product := model.Product{
		ID: id, Name: name, Code: code, Status: "active", NotifyURL: notifyURL, ReturnURL: returnURL,
		APISecretEnc: apiSecretEnc, NotifySecretEnc: notifySecretEnc, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateProduct(ctx, product); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return ProductCredentials{}, errors.New("产品代码已存在")
		}
		return ProductCredentials{}, err
	}
	return ProductCredentials{Product: product, APISecret: apiSecret, NotifySecret: notifySecret}, nil
}

func (s *Service) AuthenticateProduct(ctx context.Context, productID, timestamp, nonce, signature, rawBody string) (model.Product, error) {
	product, err := s.store.GetProduct(ctx, productID)
	if err != nil || product.Status != "active" {
		return model.Product{}, errors.New("产品身份无效或已停用")
	}
	unixTime, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return model.Product{}, errors.New("时间戳无效")
	}
	requestTime := time.Unix(unixTime, 0)
	if delta := s.now().Sub(requestTime); delta > s.config.RequestMaxSkew || delta < -s.config.RequestMaxSkew {
		return model.Product{}, errors.New("请求时间戳已过期")
	}
	if !noncePattern.MatchString(nonce) {
		return model.Product{}, errors.New("nonce 格式无效")
	}
	secret, err := s.vault.Decrypt(product.APISecretEnc)
	if err != nil {
		return model.Product{}, errors.New("产品密钥不可用")
	}
	expected := secure.HMAC256(secret, timestamp+"."+nonce+"."+rawBody)
	if !secure.ConstantEqual(signature, expected) {
		return model.Product{}, errors.New("请求签名无效")
	}
	if err := s.store.InsertNonce(ctx, product.ID, nonce, s.now().Add(s.config.RequestMaxSkew)); err != nil {
		if errors.Is(err, store.ErrReplay) {
			return model.Product{}, errors.New("请求 nonce 已使用")
		}
		return model.Product{}, err
	}
	return product, nil
}

func (s *Service) CreateOrder(ctx context.Context, product model.Product, input CreateOrderInput) (OrderResult, error) {
	if !orderNoPattern.MatchString(input.ProductOrderNo) {
		return OrderResult{}, errors.New("产品订单号格式无效")
	}
	if input.PayType != 1 && input.PayType != 2 {
		return OrderResult{}, errors.New("支付类型只能是 1 或 2")
	}
	input.GoodsName = strings.TrimSpace(input.GoodsName)
	if input.GoodsName == "" || utf8.RuneCountInString(input.GoodsName) > 50 {
		return OrderResult{}, errors.New("商品名称不能为空且最多 50 个字符")
	}
	amountCents, err := money.Parse(input.Amount)
	if err != nil {
		return OrderResult{}, err
	}
	if existing, err := s.store.GetOrderByProductNo(ctx, product.ID, input.ProductOrderNo); err == nil {
		if existing.AmountCents != amountCents || existing.PayType != input.PayType || existing.GoodsName != input.GoodsName {
			return OrderResult{}, errors.New("该产品订单号已用于不同的订单内容")
		}
		return OrderResult{Order: existing, Idempotent: true}, nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return OrderResult{}, err
	}
	orderID, err := randomID("ord_")
	if err != nil {
		return OrderResult{}, err
	}
	payID, err := randomPayID()
	if err != nil {
		return OrderResult{}, err
	}
	now := s.now()
	order := model.Order{
		ID: orderID, ProductID: product.ID, ProductName: product.Name, ProductOrderNo: input.ProductOrderNo,
		PayID: payID, PayType: input.PayType, GoodsName: input.GoodsName, AmountCents: amountCents,
		Status: "creating", PayURL: "", CreatedAt: now, UpdatedAt: now,
	}
	if err := s.store.CreateOrder(ctx, order); err != nil {
		if errors.Is(err, store.ErrConflict) {
			existing, getErr := s.store.GetOrderByProductNo(ctx, product.ID, input.ProductOrderNo)
			if getErr == nil {
				return OrderResult{Order: existing, Idempotent: true}, nil
			}
		}
		return OrderResult{}, err
	}
	publicBaseURL := s.config.PublicBaseURL + s.config.BasePath
	response, err := s.gateway.CreateOrder(ctx, epay.CreateRequest{
		PayID: payID, Param: orderID, PayType: input.PayType, Price: money.Format(amountCents), GoodsName: input.GoodsName,
		NotifyURL:  publicBaseURL + "/api/epay/notify",
		ReturnURL:  publicBaseURL + "/payment/return",
		TimeoutURL: publicBaseURL + "/payment/timeout",
	})
	if err != nil {
		_ = s.store.FailOrder(ctx, order.ID)
		return OrderResult{}, err
	}
	responseCents, err := money.Parse(response.Price)
	if err != nil || responseCents != amountCents || response.PayType != input.PayType || response.State != 0 || response.Timeout <= 0 {
		_ = s.store.FailOrder(ctx, order.ID)
		return OrderResult{}, errors.New("易支付返回的订单金额、支付类型或状态与请求不一致")
	}
	reallyCents, err := money.Parse(response.ReallyPrice)
	if err != nil {
		_ = s.store.FailOrder(ctx, order.ID)
		return OrderResult{}, fmt.Errorf("易支付返回了无效的实付金额: %w", err)
	}
	finalizedAt := s.now()
	expires := finalizedAt.Add(time.Duration(response.Timeout) * time.Minute)
	order.EpayOrderID, order.ReallyAmountCents, order.Status = response.OrderID, &reallyCents, "pending"
	order.PayURL, order.ExpiresAt, order.UpdatedAt = response.PayURL, &expires, finalizedAt
	if err := s.store.FinalizeOrder(ctx, order); err != nil {
		return OrderResult{}, err
	}
	return OrderResult{Order: order}, nil
}

func (s *Service) ProcessCallback(ctx context.Context, values url.Values) (string, bool, error) {
	mode, err := s.gateway.VerifyCallback(values)
	if err != nil {
		return "", false, err
	}
	if values.Get("param") == "" || values.Get("type") == "" || values.Get("price") == "" || values.Get("reallyPrice") == "" {
		return "", false, errors.New("回调缺少必要参数")
	}
	payType, err := strconv.Atoi(values.Get("type"))
	if err != nil {
		return "", false, errors.New("回调支付类型无效")
	}
	amountCents, err := money.Parse(values.Get("price"))
	if err != nil {
		return "", false, errors.New("回调订单金额无效")
	}
	reallyCents, err := money.Parse(values.Get("reallyPrice"))
	if err != nil {
		return "", false, errors.New("回调实付金额无效")
	}
	if _, err := s.ExpireDueOrders(ctx); err != nil {
		return "", false, err
	}
	order, err := s.store.GetOrder(ctx, values.Get("param"))
	if err != nil {
		return "", false, err
	}
	if order.ReallyAmountCents != nil && *order.ReallyAmountCents != reallyCents {
		return "", false, errors.New("回调实付金额与下单结果不一致")
	}
	product, err := s.store.GetProduct(ctx, order.ProductID)
	if err != nil {
		return "", false, err
	}
	paidAt := s.now().UTC()
	eventID, err := randomID("evt_")
	if err != nil {
		return "", false, err
	}
	event := PaymentEvent{
		ID: eventID, Type: "payment.succeeded", CreatedAt: paidAt, RelayOrderID: order.ID,
		ProductOrderNo: order.ProductOrderNo, PayID: order.PayID, EpayOrderID: order.EpayOrderID,
		PayType: payType, Amount: money.Format(amountCents), ReallyAmount: money.Format(reallyCents), Status: "paid",
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return "", false, err
	}
	raw, _ := json.Marshal(values)
	created, err := s.store.ConfirmPayment(ctx, store.PaymentConfirmation{
		OrderID: order.ID, PayID: values.Get("payId"), EpayOrderID: values.Get("orderId"),
		PayType: payType, AmountCents: amountCents, ReallyAmountCents: reallyCents,
		RawNotify: string(raw), PaidAt: paidAt,
		Delivery: model.Delivery{
			ID: eventID, OrderID: order.ID, ProductID: order.ProductID, Endpoint: product.NotifyURL,
			EventType: "payment.succeeded", Payload: string(payload), Status: "pending",
			NextAttemptAt: paidAt, CreatedAt: paidAt, UpdatedAt: paidAt,
		},
	})
	return mode, created, err
}

func (s *Service) MockPay(ctx context.Context, orderID string) error {
	if !s.config.EpayMock {
		return errors.New("模拟支付未启用")
	}
	order, err := s.store.GetOrder(ctx, orderID)
	if err != nil {
		return err
	}
	if order.Status == "paid" {
		return nil
	}
	if order.Status != "pending" {
		return errors.New("订单当前状态不可支付")
	}
	price := money.Format(order.AmountCents)
	really := price
	sign, _ := s.gateway.SignCallback(order.EpayOrderID, order.PayID, order.ID, strconv.Itoa(order.PayType), price, really)
	values := url.Values{
		"mchId": {s.gateway.MerchantID()}, "orderId": {order.EpayOrderID}, "payId": {order.PayID},
		"param": {order.ID}, "type": {strconv.Itoa(order.PayType)}, "price": {price}, "reallyPrice": {really}, "sign": {sign},
	}
	_, _, err = s.ProcessCallback(ctx, values)
	return err
}

func (s *Service) validateEndpoint(value string, required bool) error {
	if value == "" && !required {
		return nil
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("必须是完整的 HTTP(S) URL")
	}
	if s.config.Environment == "production" && parsed.Scheme != "https" {
		return errors.New("生产环境必须使用 HTTPS")
	}
	return nil
}

func randomID(prefix string) (string, error) {
	token, err := secure.RandomToken(12)
	if err != nil {
		return "", err
	}
	return prefix + token, nil
}

func randomPayID() (string, error) {
	token, err := secure.RandomToken(10)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("EP%d%s", time.Now().UnixMilli(), token), nil
}
