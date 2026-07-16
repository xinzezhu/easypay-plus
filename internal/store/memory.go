package store

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/easypay-plus/easypay-plus/internal/model"
)

type Memory struct {
	mu         sync.Mutex
	products   map[string]model.Product
	orders     map[string]model.Order
	nonces     map[string]time.Time
	deliveries map[string]model.Delivery
}

func NewMemory() *Memory {
	return &Memory{
		products: make(map[string]model.Product), orders: make(map[string]model.Order),
		nonces: make(map[string]time.Time), deliveries: make(map[string]model.Delivery),
	}
}

func (s *Memory) Close() error                 { return nil }
func (s *Memory) Health(context.Context) error { return nil }

func (s *Memory) CreateProduct(_ context.Context, p model.Product) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.products {
		if existing.Code == p.Code {
			return ErrConflict
		}
	}
	s.products[p.ID] = p
	return nil
}

func (s *Memory) ListProducts(context.Context) ([]model.Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]model.Product, 0, len(s.products))
	for _, p := range s.products {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result, nil
}

func (s *Memory) GetProduct(_ context.Context, id string) (model.Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.products[id]
	if !ok {
		return model.Product{}, ErrNotFound
	}
	return p, nil
}

func (s *Memory) SetProductStatus(_ context.Context, id, status string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.products[id]
	if !ok {
		return ErrNotFound
	}
	p.Status, p.UpdatedAt = status, now
	if status == "disabled" {
		p.DisabledAt = &now
	} else {
		p.DisabledAt = nil
	}
	s.products[id] = p
	return nil
}

func (s *Memory) InsertNonce(_ context.Context, productID, nonce string, expires time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := productID + ":" + nonce
	if expiry, ok := s.nonces[key]; ok && expiry.After(time.Now()) {
		return ErrReplay
	}
	s.nonces[key] = expires
	return nil
}

func (s *Memory) CreateOrder(_ context.Context, o model.Order) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.orders {
		if existing.PayID == o.PayID || (existing.ProductID == o.ProductID && existing.ProductOrderNo == o.ProductOrderNo) {
			return ErrConflict
		}
	}
	s.orders[o.ID] = o
	return nil
}

func (s *Memory) GetOrder(_ context.Context, id string) (model.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok {
		return model.Order{}, ErrNotFound
	}
	return s.enrichOrder(o), nil
}

func (s *Memory) GetOrderByProductNo(_ context.Context, productID, productOrderNo string) (model.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, o := range s.orders {
		if o.ProductID == productID && o.ProductOrderNo == productOrderNo {
			return s.enrichOrder(o), nil
		}
	}
	return model.Order{}, ErrNotFound
}

func (s *Memory) FinalizeOrder(_ context.Context, o model.Order) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.orders[o.ID]
	if !ok {
		return ErrNotFound
	}
	if current.Status != "creating" {
		return ErrConflict
	}
	current.EpayOrderID, current.ReallyAmountCents, current.Status = o.EpayOrderID, o.ReallyAmountCents, o.Status
	current.PayURL, current.ExpiresAt, current.UpdatedAt = o.PayURL, o.ExpiresAt, o.UpdatedAt
	s.orders[o.ID] = current
	return nil
}

func (s *Memory) FailOrder(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[id]
	if !ok {
		return ErrNotFound
	}
	if o.Status == "creating" {
		o.Status, o.UpdatedAt = "failed", time.Now()
		s.orders[id] = o
	}
	return nil
}

func (s *Memory) ExpireDueOrders(_ context.Context, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var expired int64
	for id, order := range s.orders {
		if order.Status != "pending" || order.ExpiresAt == nil || order.ExpiresAt.After(now) {
			continue
		}
		order.Status, order.UpdatedAt = "expired", now
		s.orders[id] = order
		expired++
	}
	return expired, nil
}

func (s *Memory) ConfirmPayment(_ context.Context, c PaymentConfirmation) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.orders[c.OrderID]
	if !ok {
		return false, ErrNotFound
	}
	if (c.PayID != "" && c.PayID != o.PayID) || (c.EpayOrderID != "" && o.EpayOrderID != "" && c.EpayOrderID != o.EpayOrderID) ||
		c.PayType != o.PayType || c.AmountCents != o.AmountCents || c.Delivery.ProductID != o.ProductID {
		return false, ErrMismatch
	}
	created := false
	if o.Status != "paid" {
		if o.Status != "pending" {
			return false, ErrConflict
		}
		o.Status, o.ReallyAmountCents, o.PaidAt, o.UpdatedAt = "paid", &c.ReallyAmountCents, &c.PaidAt, c.PaidAt
		s.orders[o.ID] = o
		created = true
	}
	for _, d := range s.deliveries {
		if d.OrderID == o.ID && d.EventType == c.Delivery.EventType {
			return created, nil
		}
	}
	s.deliveries[c.Delivery.ID] = c.Delivery
	return created, nil
}

func (s *Memory) ListOrders(_ context.Context, status, productID string, limit int) ([]model.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]model.Order, 0)
	for _, o := range s.orders {
		if (status == "" || o.Status == status) && (productID == "" || o.ProductID == productID) {
			result = append(result, s.enrichOrder(o))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

func (s *Memory) Stats(context.Context) (model.Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := model.Stats{Products: int64(len(s.products)), Orders: int64(len(s.orders))}
	for _, p := range s.products {
		if p.Status == "active" {
			stats.ActiveProducts++
		}
	}
	for _, o := range s.orders {
		switch o.Status {
		case "paid":
			stats.PaidOrders++
			if o.ReallyAmountCents != nil {
				stats.PaidCents += *o.ReallyAmountCents
			}
		case "pending", "creating":
			stats.PendingOrders++
		}
	}
	for _, d := range s.deliveries {
		if d.Status == "failed" {
			stats.FailedDelivery++
		}
	}
	return stats, nil
}

func (s *Memory) ClaimDelivery(_ context.Context, now time.Time) (*model.Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var chosen *model.Delivery
	for id, candidate := range s.deliveries {
		due := (candidate.Status == "pending" || candidate.Status == "retrying") && !candidate.NextAttemptAt.After(now)
		expiredLock := candidate.Status == "processing" && candidate.LockedUntil != nil && !candidate.LockedUntil.After(now)
		if due || expiredLock {
			copy := candidate
			if chosen == nil || copy.NextAttemptAt.Before(chosen.NextAttemptAt) {
				chosen = &copy
			}
			_ = id
		}
	}
	if chosen == nil {
		return nil, nil
	}
	locked := now.Add(time.Minute)
	chosen.Status, chosen.LockedUntil = "processing", &locked
	if product, ok := s.products[chosen.ProductID]; ok {
		chosen.NotifySecretEnc = product.NotifySecretEnc
	}
	s.deliveries[chosen.ID] = *chosen
	copy := *chosen
	return &copy, nil
}

func (s *Memory) CompleteDelivery(_ context.Context, id string, success, terminal bool, next time.Time, httpStatus *int, lastError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.deliveries[id]
	if !ok {
		return ErrNotFound
	}
	d.AttemptCount++
	d.LockedUntil, d.HTTPStatus, d.LastError, d.NextAttemptAt, d.UpdatedAt = nil, httpStatus, lastError, next, time.Now()
	if success {
		now := time.Now()
		d.Status, d.DeliveredAt = "delivered", &now
	} else if terminal {
		d.Status = "failed"
	} else {
		d.Status = "retrying"
	}
	s.deliveries[id] = d
	return nil
}

func (s *Memory) RetryDelivery(_ context.Context, id string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.deliveries[id]
	if !ok {
		return ErrNotFound
	}
	d.Status, d.AttemptCount, d.NextAttemptAt, d.LockedUntil, d.LastError = "retrying", 0, now, nil, ""
	s.deliveries[id] = d
	return nil
}

func (s *Memory) enrichOrder(o model.Order) model.Order {
	if product, ok := s.products[o.ProductID]; ok {
		o.ProductName = product.Name
	}
	for _, d := range s.deliveries {
		if d.OrderID == o.ID && d.EventType == "payment.succeeded" {
			o.DeliveryID, o.DeliveryStatus, o.DeliveryAttempts, o.DeliveryLastError = d.ID, d.Status, d.AttemptCount, d.LastError
		}
	}
	return o
}
