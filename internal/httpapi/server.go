package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/easypay-plus/easypay-plus/internal/config"
	"github.com/easypay-plus/easypay-plus/internal/model"
	"github.com/easypay-plus/easypay-plus/internal/money"
	"github.com/easypay-plus/easypay-plus/internal/service"
	"github.com/easypay-plus/easypay-plus/internal/store"
	webassets "github.com/easypay-plus/easypay-plus/web"
)

type Server struct {
	service *service.Service
	config  config.Config
	logger  *slog.Logger
	handler http.Handler
}

type orderResponse struct {
	ID                string     `json:"id"`
	ProductID         string     `json:"productId"`
	ProductName       string     `json:"productName,omitempty"`
	ProductOrderNo    string     `json:"productOrderNo"`
	PayID             string     `json:"payId"`
	EpayOrderID       string     `json:"epayOrderId,omitempty"`
	PayType           int        `json:"payType"`
	GoodsName         string     `json:"goodsName"`
	Amount            string     `json:"amount"`
	ReallyAmount      string     `json:"reallyAmount,omitempty"`
	Status            string     `json:"status"`
	PayURL            string     `json:"payUrl,omitempty"`
	ExpiresAt         *time.Time `json:"expiresAt,omitempty"`
	PaidAt            *time.Time `json:"paidAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	DeliveryID        string     `json:"deliveryId,omitempty"`
	DeliveryStatus    string     `json:"deliveryStatus,omitempty"`
	DeliveryAttempts  int        `json:"deliveryAttempts,omitempty"`
	DeliveryLastError string     `json:"deliveryLastError,omitempty"`
}

func New(service *service.Service, cfg config.Config, logger *slog.Logger) *Server {
	s := &Server{service: service, config: cfg, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.Handle("GET /api/admin/overview", s.admin(http.HandlerFunc(s.handleOverview)))
	mux.Handle("GET /api/admin/products", s.admin(http.HandlerFunc(s.handleListProducts)))
	mux.Handle("POST /api/admin/products", s.admin(http.HandlerFunc(s.handleCreateProduct)))
	mux.Handle("PATCH /api/admin/products/{id}/status", s.admin(http.HandlerFunc(s.handleProductStatus)))
	mux.Handle("GET /api/admin/orders", s.admin(http.HandlerFunc(s.handleListOrders)))
	mux.Handle("POST /api/admin/deliveries/{id}/retry", s.admin(http.HandlerFunc(s.handleRetryDelivery)))
	mux.HandleFunc("POST /api/v1/orders", s.handleCreateOrder)
	mux.HandleFunc("GET /api/v1/orders/{orderNo}", s.handleGetProductOrder)
	mux.HandleFunc("GET /api/epay/notify", s.handleEpayNotify)
	mux.HandleFunc("POST /api/epay/notify", s.handleEpayNotify)
	mux.HandleFunc("GET /payment/return", s.handlePaymentReturn)
	mux.HandleFunc("GET /payment/timeout", s.handlePaymentTimeout)
	mux.HandleFunc("GET /api/mock/orders/{id}", s.handleMockOrder)
	mux.HandleFunc("POST /api/mock/orders/{id}/pay", s.handleMockPay)
	mux.HandleFunc("GET /", s.handleSPA)
	s.handler = s.securityHeaders(s.requestLog(mux))
	return s
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.service.Store().Health(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "数据库连接不可用")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "mock": s.config.EpayMock, "database": s.config.DBDriver})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	stats, err := s.service.Store().Stats(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stats": stats, "mock": s.config.EpayMock, "database": s.config.DBDriver})
}

func (s *Server) handleListProducts(w http.ResponseWriter, r *http.Request) {
	products, err := s.service.Store().ListProducts(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": products})
}

func (s *Server) handleCreateProduct(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name      string `json:"name"`
		Code      string `json:"code"`
		NotifyURL string `json:"notifyUrl"`
		ReturnURL string `json:"returnUrl"`
	}
	if err := readJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	credentials, err := s.service.CreateProduct(r.Context(), request.Name, request.Code, request.NotifyURL, request.ReturnURL)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_product", err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, credentials)
}

func (s *Server) handleProductStatus(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Status string `json:"status"`
	}
	if err := readJSON(w, r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if request.Status != "active" && request.Status != "disabled" {
		writeError(w, http.StatusUnprocessableEntity, "invalid_status", "状态只能是 active 或 disabled")
		return
	}
	if err := s.service.Store().SetProductStatus(r.Context(), r.PathValue("id"), request.Status, time.Now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "产品不存在")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": request.Status})
}

func (s *Server) handleListOrders(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	orders, err := s.service.Store().ListOrders(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("productId"), limit)
	if err != nil {
		s.internalError(w, err)
		return
	}
	items := make([]orderResponse, 0, len(orders))
	for _, order := range orders {
		items = append(items, presentOrder(order))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleRetryDelivery(w http.ResponseWriter, r *http.Request) {
	if err := s.service.Store().RetryDelivery(r.Context(), r.PathValue("id"), time.Now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "通知任务不存在")
			return
		}
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "retrying"})
}

func (s *Server) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	rawBody, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "请求体过大或不可读")
		return
	}
	product, err := s.authenticateProduct(r, string(rawBody))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}
	var input service.CreateOrderInput
	decoder := json.NewDecoder(strings.NewReader(string(rawBody)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "JSON 请求体无效")
		return
	}
	result, err := s.service.CreateOrder(r.Context(), product, input)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "order_rejected", err.Error())
		return
	}
	status := http.StatusCreated
	if result.Idempotent {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"order": presentOrder(result.Order), "idempotent": result.Idempotent})
}

func (s *Server) handleGetProductOrder(w http.ResponseWriter, r *http.Request) {
	product, err := s.authenticateProduct(r, "")
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", err.Error())
		return
	}
	order, err := s.service.Store().GetOrderByProductNo(r.Context(), product.ID, r.PathValue("orderNo"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "订单不存在")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": presentOrder(order)})
}

func (s *Server) handleEpayNotify(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	if r.Method == http.MethodPost {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "fail", http.StatusBadRequest)
			return
		}
		values = r.Form
	}
	mode, created, err := s.service.ProcessCallback(r.Context(), values)
	if err != nil {
		s.logger.Warn("epay callback rejected", "error", err)
		http.Error(w, "fail", http.StatusBadRequest)
		return
	}
	s.logger.Info("epay callback accepted", "order", values.Get("param"), "signField", mode, "newPayment", created)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "success")
}

func (s *Server) handlePaymentReturn(w http.ResponseWriter, r *http.Request) {
	s.redirectAfterPayment(w, r, false)
}

func (s *Server) handlePaymentTimeout(w http.ResponseWriter, r *http.Request) {
	s.redirectAfterPayment(w, r, true)
}

func (s *Server) redirectAfterPayment(w http.ResponseWriter, r *http.Request, timeout bool) {
	orderID := r.URL.Query().Get("param")
	if orderID == "" {
		orderID = r.URL.Query().Get("order")
	}
	order, err := s.service.Store().GetOrder(r.Context(), orderID)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	product, err := s.service.Store().GetProduct(r.Context(), order.ProductID)
	if err != nil || product.ReturnURL == "" {
		http.Redirect(w, r, s.config.BasePath+"/?order="+url.QueryEscape(order.ID), http.StatusFound)
		return
	}
	target, err := url.Parse(product.ReturnURL)
	if err != nil {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	query := target.Query()
	query.Set("relayOrderId", order.ID)
	query.Set("productOrderNo", order.ProductOrderNo)
	if timeout {
		query.Set("status", "timeout")
	} else {
		query.Set("status", order.Status)
	}
	target.RawQuery = query.Encode()
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *Server) handleMockOrder(w http.ResponseWriter, r *http.Request) {
	if !s.config.EpayMock {
		writeError(w, http.StatusNotFound, "not_found", "页面不存在")
		return
	}
	order, err := s.service.Store().GetOrder(r.Context(), r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "订单不存在")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"order": presentOrder(order)})
}

func (s *Server) handleMockPay(w http.ResponseWriter, r *http.Request) {
	if !s.config.EpayMock {
		writeError(w, http.StatusNotFound, "not_found", "页面不存在")
		return
	}
	if err := s.service.MockPay(r.Context(), r.PathValue("id")); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "payment_failed", err.Error())
		return
	}
	order, _ := s.service.Store().GetOrder(r.Context(), r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]any{"order": presentOrder(order)})
}

func (s *Server) authenticateProduct(r *http.Request, rawBody string) (model.Product, error) {
	return s.service.AuthenticateProduct(r.Context(), r.Header.Get("X-App-Id"), r.Header.Get("X-Timestamp"),
		r.Header.Get("X-Nonce"), r.Header.Get("X-Signature"), rawBody)
}

func (s *Server) admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provided := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if len(provided) != len(s.config.AdminToken) || subtle.ConstantTimeCompare([]byte(provided), []byte(s.config.AdminToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "管理令牌无效")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" || strings.HasPrefix(path, "mock-pay/") {
		path = "index.html"
	}
	content, err := webassets.Assets.ReadFile(path)
	if err != nil {
		content, err = webassets.Assets.ReadFile("index.html")
		if err != nil {
			http.Error(w, "frontend unavailable", http.StatusInternalServerError)
			return
		}
		path = "index.html"
	}
	if path == "index.html" {
		content = []byte(strings.ReplaceAll(string(content), "__BASE_PATH__", s.config.BasePath))
	}
	contentType := mime.TypeByExtension(filepath.Ext(path))
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	if path == "index.html" {
		w.Header().Set("Cache-Control", "no-store")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=3600")
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' https://unpkg.com; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		if !strings.HasPrefix(r.URL.Path, "/api/health") {
			s.logger.Debug("http request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started))
		}
	})
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.logger.Error("request failed", "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "服务暂时不可用")
}

func presentOrder(order model.Order) orderResponse {
	response := orderResponse{
		ID: order.ID, ProductID: order.ProductID, ProductName: order.ProductName, ProductOrderNo: order.ProductOrderNo,
		PayID: order.PayID, EpayOrderID: order.EpayOrderID, PayType: order.PayType, GoodsName: order.GoodsName,
		Amount: money.Format(order.AmountCents), Status: order.Status, PayURL: order.PayURL, ExpiresAt: order.ExpiresAt,
		PaidAt: order.PaidAt, CreatedAt: order.CreatedAt, DeliveryID: order.DeliveryID,
		DeliveryStatus: order.DeliveryStatus, DeliveryAttempts: order.DeliveryAttempts, DeliveryLastError: order.DeliveryLastError,
	}
	if order.ReallyAmountCents != nil {
		response.ReallyAmount = money.Format(*order.ReallyAmountCents)
	}
	return response
}

func readJSON(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("JSON 请求体无效")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("请求体只能包含一个 JSON 对象")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
