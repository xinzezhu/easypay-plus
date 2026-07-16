package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/easypay-plus/easypay-plus/internal/secure"
)

var retryDelays = []time.Duration{
	1 * time.Minute, 5 * time.Minute, 10 * time.Minute, 30 * time.Minute,
	60 * time.Minute, 180 * time.Minute, 360 * time.Minute,
}

type DeliveryWorker struct {
	service *Service
	client  *http.Client
	logger  *slog.Logger
}

func NewDeliveryWorker(service *Service, logger *slog.Logger) *DeliveryWorker {
	return &DeliveryWorker{
		service: service,
		client: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
		logger: logger,
	}
}

func (w *DeliveryWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := w.ProcessOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.logger.Error("delivery worker failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *DeliveryWorker) ProcessOnce(ctx context.Context) error {
	delivery, err := w.service.store.ClaimDelivery(ctx, time.Now())
	if err != nil || delivery == nil {
		return err
	}
	secret, err := w.service.vault.Decrypt(delivery.NotifySecretEnc)
	if err != nil {
		return w.recordFailure(ctx, delivery.ID, delivery.AttemptCount, nil, "通知密钥不可用", err)
	}
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := secure.HMAC256(secret, timestamp+"."+delivery.ID+"."+delivery.Payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, delivery.Endpoint, strings.NewReader(delivery.Payload))
	if err != nil {
		return w.recordFailure(ctx, delivery.ID, delivery.AttemptCount, nil, "通知请求无效", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Easypay-Event-Id", delivery.ID)
	request.Header.Set("X-Easypay-Timestamp", timestamp)
	request.Header.Set("X-Easypay-Signature", signature)
	response, err := w.client.Do(request)
	if err != nil {
		return w.recordFailure(ctx, delivery.ID, delivery.AttemptCount, nil, err.Error(), nil)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<10))
	status := response.StatusCode
	if readErr == nil && status >= 200 && status < 300 && strings.EqualFold(strings.TrimSpace(string(body)), "success") {
		return w.service.store.CompleteDelivery(ctx, delivery.ID, true, false, time.Now(), &status, "")
	}
	message := fmt.Sprintf("HTTP %d: %s", status, strings.TrimSpace(string(body)))
	if readErr != nil {
		message = readErr.Error()
	}
	return w.recordFailure(ctx, delivery.ID, delivery.AttemptCount, &status, message, nil)
}

func (w *DeliveryWorker) recordFailure(ctx context.Context, id string, attempts int, status *int, message string, cause error) error {
	terminal := attempts >= len(retryDelays)
	next := time.Now()
	if !terminal {
		next = next.Add(retryDelays[attempts])
	}
	if err := w.service.store.CompleteDelivery(ctx, id, false, terminal, next, status, truncate(message, 1000)); err != nil {
		return err
	}
	if cause != nil {
		return cause
	}
	return nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
