package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/easypay-plus/easypay-plus/internal/config"
	"github.com/easypay-plus/easypay-plus/internal/epay"
	"github.com/easypay-plus/easypay-plus/internal/secure"
	"github.com/easypay-plus/easypay-plus/internal/service"
	"github.com/easypay-plus/easypay-plus/internal/store"
)

func TestCompleteMockPaymentFlow(t *testing.T) {
	var receiver struct {
		sync.Mutex
		body      string
		eventID   string
		timestamp string
		signature string
	}
	notifyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receiver.Lock()
		receiver.body = string(body)
		receiver.eventID = r.Header.Get("X-Easypay-Event-Id")
		receiver.timestamp = r.Header.Get("X-Easypay-Timestamp")
		receiver.signature = r.Header.Get("X-Easypay-Signature")
		receiver.Unlock()
		_, _ = w.Write([]byte("success"))
	}))
	defer notifyServer.Close()

	cfg := config.Config{
		Environment: "test", PublicBaseURL: "http://easypay.test", AdminToken: "admin-test-token",
		MasterKey: "test-master-key", DBDriver: "memory", EpayMock: true,
		EpayCallbackSignMode: "auto", RequestMaxSkew: 5 * time.Minute,
	}
	storage := store.NewMemory()
	vault := secure.NewVault(cfg.MasterKey)
	gateway := epay.New(epay.Config{Mock: true, PublicBaseURL: cfg.PublicBaseURL, CallbackSignMode: "auto"})
	appService := service.New(storage, gateway, vault, cfg)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(New(appService, cfg, logger).Handler())
	defer server.Close()

	productBody := fmt.Sprintf(`{"name":"会员中心","code":"member_center","notifyUrl":%q}`, notifyServer.URL)
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/admin/products", bytes.NewBufferString(productBody))
	request.Header.Set("Authorization", "Bearer "+cfg.AdminToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create product status: %d", response.StatusCode)
	}
	var credentials struct {
		Product struct {
			ID string `json:"id"`
		} `json:"product"`
		APISecret    string `json:"apiSecret"`
		NotifySecret string `json:"notifySecret"`
	}
	if err := json.NewDecoder(response.Body).Decode(&credentials); err != nil {
		t.Fatal(err)
	}

	orderBody := `{"productOrderNo":"MEMBER-1001","amount":"9.90","payType":2,"goodsName":"月度会员"}`
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "test_nonce_1234567890"
	signature := secure.HMAC256(credentials.APISecret, timestamp+"."+nonce+"."+orderBody)
	request, _ = http.NewRequest(http.MethodPost, server.URL+"/api/v1/orders", bytes.NewBufferString(orderBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-App-Id", credentials.Product.ID)
	request.Header.Set("X-Timestamp", timestamp)
	request.Header.Set("X-Nonce", nonce)
	request.Header.Set("X-Signature", signature)
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("create order status=%d body=%s", response.StatusCode, body)
	}
	var orderResult struct {
		Order struct{ ID, Status, PayURL string } `json:"order"`
	}
	if err := json.NewDecoder(response.Body).Decode(&orderResult); err != nil {
		t.Fatal(err)
	}
	if orderResult.Order.Status != "pending" || orderResult.Order.ID == "" {
		t.Fatalf("unexpected order: %+v", orderResult.Order)
	}

	response, err = http.Post(server.URL+"/api/mock/orders/"+orderResult.Order.ID+"/pay", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("mock pay status: %d", response.StatusCode)
	}

	worker := service.NewDeliveryWorker(appService, logger)
	if err := worker.ProcessOnce(t.Context()); err != nil {
		t.Fatal(err)
	}

	receiver.Lock()
	defer receiver.Unlock()
	if receiver.body == "" || receiver.eventID == "" {
		t.Fatal("product notification was not delivered")
	}
	expectedSignature := secure.HMAC256(credentials.NotifySecret, receiver.timestamp+"."+receiver.eventID+"."+receiver.body)
	if !secure.ConstantEqual(receiver.signature, expectedSignature) {
		t.Fatal("product notification signature mismatch")
	}
	var event struct {
		Type           string `json:"type"`
		ProductOrderNo string `json:"productOrderNo"`
		Amount         string `json:"amount"`
		Status         string `json:"status"`
	}
	if err := json.Unmarshal([]byte(receiver.body), &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != "payment.succeeded" || event.ProductOrderNo != "MEMBER-1001" || event.Amount != "9.90" || event.Status != "paid" {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestRealCheckoutReturnsPaymentPageAndQRCode(t *testing.T) {
	cfg := config.Config{
		Environment: "test", PublicBaseURL: "https://pay.example.test", AdminToken: "admin-test-token",
		MasterKey: "test-master-key", DBDriver: "memory", EpayMock: false,
		EpayCallbackSignMode: "auto", RequestMaxSkew: 5 * time.Minute,
	}
	storage := store.NewMemory()
	vault := secure.NewVault(cfg.MasterKey)
	// The gateway is mocked while the HTTP server runs in real checkout mode.
	gateway := epay.New(epay.Config{Mock: true, PublicBaseURL: cfg.PublicBaseURL, CallbackSignMode: "auto"})
	appService := service.New(storage, gateway, vault, cfg)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server := httptest.NewServer(New(appService, cfg, logger).Handler())
	defer server.Close()

	productBody := `{"name":"会员中心","code":"member_center","notifyUrl":"https://product.example.test/payment/notify"}`
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/admin/products", bytes.NewBufferString(productBody))
	request.Header.Set("Authorization", "Bearer "+cfg.AdminToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var credentials struct {
		Product struct {
			ID string `json:"id"`
		} `json:"product"`
		APISecret string `json:"apiSecret"`
	}
	if err := json.NewDecoder(response.Body).Decode(&credentials); err != nil {
		t.Fatal(err)
	}

	orderBody := `{"productOrderNo":"CHECKOUT-1001","amount":"9.90","payType":2,"goodsName":"月度会员"}`
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := "checkout_nonce_1234567890"
	request, _ = http.NewRequest(http.MethodPost, server.URL+"/api/v1/orders", bytes.NewBufferString(orderBody))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-App-Id", credentials.Product.ID)
	request.Header.Set("X-Timestamp", timestamp)
	request.Header.Set("X-Nonce", nonce)
	request.Header.Set("X-Signature", secure.HMAC256(credentials.APISecret, timestamp+"."+nonce+"."+orderBody))
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create order status: %d", response.StatusCode)
	}
	var created struct {
		Order struct {
			ID     string `json:"id"`
			PayURL string `json:"payUrl"`
		} `json:"order"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Order.ID == "" || created.Order.PayURL != cfg.PublicBaseURL+"/pay/"+created.Order.ID {
		t.Fatalf("unexpected checkout URL: %+v", created.Order)
	}

	response, err = http.Get(server.URL + "/api/pay/orders/" + created.Order.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("checkout order status: %d", response.StatusCode)
	}
	var checkout struct {
		Order struct {
			Status    string `json:"status"`
			Amount    string `json:"amount"`
			GoodsName string `json:"goodsName"`
		} `json:"order"`
	}
	if err := json.NewDecoder(response.Body).Decode(&checkout); err != nil {
		t.Fatal(err)
	}
	if checkout.Order.Status != "pending" || checkout.Order.Amount != "9.90" || checkout.Order.GoodsName != "月度会员" {
		t.Fatalf("unexpected checkout order: %+v", checkout.Order)
	}

	response, err = http.Get(server.URL + "/pay/" + created.Order.ID + "/qrcode.png")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("qrcode status=%d type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}
	png, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(png) < 8 || !bytes.Equal(png[:8], []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}) {
		t.Fatal("checkout response was not a PNG")
	}
}

func TestCheckoutPageReferencesVersionedAssets(t *testing.T) {
	cfg := config.Config{
		Environment: "test", PublicBaseURL: "https://pay.example.test", AdminToken: "admin-test-token",
		MasterKey: "test-master-key", DBDriver: "memory", EpayMock: false,
		EpayCallbackSignMode: "auto", RequestMaxSkew: 5 * time.Minute,
	}
	storage := store.NewMemory()
	vault := secure.NewVault(cfg.MasterKey)
	gateway := epay.New(epay.Config{Mock: true, PublicBaseURL: cfg.PublicBaseURL, CallbackSignMode: "auto"})
	server := httptest.NewServer(New(service.New(storage, gateway, vault, cfg), cfg, slog.New(slog.NewTextHandler(io.Discard, nil))).Handler())
	defer server.Close()

	response, err := http.Get(server.URL + "/pay/ord_example")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(content)
	if response.StatusCode != http.StatusOK || !strings.Contains(body, "app.js?v=") || !strings.Contains(body, "styles.css?v=") || strings.Contains(body, "__ASSET_VERSION__") {
		t.Fatalf("checkout assets are not versioned: status=%d body=%s", response.StatusCode, body)
	}
}

func TestPaymentQRCodePayload(t *testing.T) {
	tests := []struct {
		name   string
		payURL string
		want   string
	}{
		{
			name:   "EasyPay QR image URL",
			payURL: "https://epay.jylt.cc/api/enQrcode?url=https%3A%2F%2Fqr.alipay.com%2Ffkx19707n0gowztqbpgf7b0",
			want:   "https://qr.alipay.com/fkx19707n0gowztqbpgf7b0",
		},
		{
			name:   "direct QR payload",
			payURL: "https://qr.alipay.com/fkx19707n0gowztqbpgf7b0",
			want:   "https://qr.alipay.com/fkx19707n0gowztqbpgf7b0",
		},
		{
			name:   "missing nested payload",
			payURL: "https://epay.jylt.cc/api/enQrcode",
			want:   "https://epay.jylt.cc/api/enQrcode",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := paymentQRCodePayload(test.payURL); got != test.want {
				t.Fatalf("paymentQRCodePayload(%q) = %q, want %q", test.payURL, got, test.want)
			}
		})
	}
}
