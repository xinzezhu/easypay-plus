package epay

import (
	"context"
	"crypto/md5"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	BaseURL          string
	MerchantID       string
	Secret           string
	Mock             bool
	PublicBaseURL    string
	CallbackSignMode string
}

type Client struct {
	config Config
	http   *http.Client
}

type CreateRequest struct {
	PayID      string
	Param      string
	PayType    int
	Price      string
	GoodsName  string
	NotifyURL  string
	ReturnURL  string
	TimeoutURL string
}

type CreateResponse struct {
	PayID       string
	OrderID     string
	PayType     int
	Price       string
	ReallyPrice string
	PayURL      string
	State       int
	Timeout     int
}

type callbackEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func New(config Config) *Client {
	return &Client{
		config: config,
		http:   &http.Client{Timeout: 12 * time.Second},
	}
}

func (c *Client) CreateOrder(ctx context.Context, request CreateRequest) (CreateResponse, error) {
	if c.config.Mock {
		return CreateResponse{
			PayID: request.PayID, OrderID: "MOCK-" + strings.TrimPrefix(request.Param, "ord_"),
			PayType: request.PayType, Price: request.Price, ReallyPrice: request.Price,
			PayURL: c.config.PublicBaseURL + "/mock-pay/" + request.Param, State: 0, Timeout: 15,
		}, nil
	}
	form := url.Values{
		"mchId":      {c.config.MerchantID},
		"payId":      {request.PayID},
		"param":      {request.Param},
		"type":       {strconv.Itoa(request.PayType)},
		"price":      {request.Price},
		"goodsName":  {request.GoodsName},
		"isHtml":     {"0"},
		"notifyUrl":  {request.NotifyURL},
		"returnUrl":  {request.ReturnURL},
		"timeoutUrl": {request.TimeoutURL},
	}
	form.Set("sign", SignCreate(request.PayID, request.Param, strconv.Itoa(request.PayType), request.Price, c.config.Secret))
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.BaseURL+"/api/createOrder", strings.NewReader(form.Encode()))
	if err != nil {
		return CreateResponse{}, err
	}
	httpRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return CreateResponse{}, fmt.Errorf("create epay order: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return CreateResponse{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return CreateResponse{}, fmt.Errorf("epay returned HTTP %d", response.StatusCode)
	}
	var envelope callbackEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return CreateResponse{}, fmt.Errorf("decode epay response: %w", err)
	}
	if envelope.Code != 1 {
		return CreateResponse{}, fmt.Errorf("epay rejected order: %s", envelope.Msg)
	}
	var data struct {
		PayID       string      `json:"payId"`
		OrderID     string      `json:"orderId"`
		PayType     int         `json:"payType"`
		Price       json.Number `json:"price"`
		ReallyPrice json.Number `json:"reallyPrice"`
		PayURL      string      `json:"payUrl"`
		State       int         `json:"state"`
		Timeout     int         `json:"timeOut"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(envelope.Data)))
	decoder.UseNumber()
	if err := decoder.Decode(&data); err != nil {
		return CreateResponse{}, fmt.Errorf("decode epay order data: %w", err)
	}
	if data.PayID != request.PayID || data.OrderID == "" || data.PayURL == "" {
		return CreateResponse{}, errors.New("epay response is missing or mismatching order identity")
	}
	return CreateResponse{
		PayID: data.PayID, OrderID: data.OrderID, PayType: data.PayType,
		Price: data.Price.String(), ReallyPrice: data.ReallyPrice.String(), PayURL: data.PayURL,
		State: data.State, Timeout: data.Timeout,
	}, nil
}

func (c *Client) MerchantID() string {
	if c.config.Mock {
		return "mock-merchant"
	}
	return c.config.MerchantID
}

func (c *Client) SignCallback(orderID, payID, param, payType, price, reallyPrice string) (string, string) {
	secret := c.config.Secret
	if c.config.Mock {
		secret = "mock-secret"
	}
	field := orderID
	mode := c.config.CallbackSignMode
	if mode == "payId" || (mode == "auto" && orderID == "") {
		field = payID
		mode = "payId"
	} else {
		mode = "orderId"
	}
	return md5Hex(field + param + payType + price + reallyPrice + secret), mode
}

func (c *Client) VerifyCallback(values url.Values) (string, error) {
	if values.Get("mchId") != c.MerchantID() {
		return "", errors.New("merchant id mismatch")
	}
	provided := strings.ToLower(values.Get("sign"))
	if len(provided) != 32 {
		return "", errors.New("invalid callback signature")
	}
	secret := c.config.Secret
	if c.config.Mock {
		secret = "mock-secret"
	}
	base := values.Get("param") + values.Get("type") + values.Get("price") + values.Get("reallyPrice") + secret
	candidates := make([]struct{ mode, value string }, 0, 2)
	mode := c.config.CallbackSignMode
	if (mode == "auto" || mode == "orderId") && values.Get("orderId") != "" {
		candidates = append(candidates, struct{ mode, value string }{"orderId", values.Get("orderId") + base})
	}
	if (mode == "auto" || mode == "payId") && values.Get("payId") != "" {
		candidates = append(candidates, struct{ mode, value string }{"payId", values.Get("payId") + base})
	}
	for _, candidate := range candidates {
		expected := md5Hex(candidate.value)
		if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1 {
			return candidate.mode, nil
		}
	}
	return "", errors.New("callback signature mismatch")
}

func SignCreate(payID, param, payType, price, secret string) string {
	return md5Hex(payID + param + payType + price + secret)
}

func md5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}
