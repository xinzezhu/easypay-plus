package store

import (
	"testing"
	"time"

	"github.com/easypay-plus/easypay-plus/internal/model"
)

func TestMemoryExpiresDuePendingOrders(t *testing.T) {
	storage := NewMemory()
	now := time.Now().UTC()
	expiresAt := now.Add(-time.Second)
	order := model.Order{
		ID: "ord_expired", ProductID: "prod_test", ProductOrderNo: "ORDER-1", PayID: "pay_expired",
		PayType: 2, GoodsName: "测试订单", AmountCents: 100, Status: "pending", ExpiresAt: &expiresAt,
	}
	if err := storage.CreateOrder(t.Context(), order); err != nil {
		t.Fatal(err)
	}
	count, err := storage.ExpireDueOrders(t.Context(), now)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expired count = %d, want 1", count)
	}
	updated, err := storage.GetOrder(t.Context(), order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != "expired" {
		t.Fatalf("status = %q, want expired", updated.Status)
	}
}
