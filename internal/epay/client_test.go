package epay

import (
	"net/url"
	"testing"
)

func TestVerifyCallbackSupportsDocumentedOrderID(t *testing.T) {
	client := New(Config{MerchantID: "m1", Secret: "secret", CallbackSignMode: "auto"})
	values := url.Values{
		"mchId": {"m1"}, "orderId": {"cloud-1"}, "payId": {"pay-1"}, "param": {"ord-1"},
		"type": {"2"}, "price": {"0.10"}, "reallyPrice": {"0.10"},
	}
	values.Set("sign", md5Hex("cloud-1ord-120.100.10secret"))
	mode, err := client.VerifyCallback(values)
	if err != nil || mode != "orderId" {
		t.Fatalf("VerifyCallback() mode=%q err=%v", mode, err)
	}
}

func TestVerifyCallbackSupportsExamplePayID(t *testing.T) {
	client := New(Config{MerchantID: "m1", Secret: "secret", CallbackSignMode: "auto"})
	values := url.Values{
		"mchId": {"m1"}, "payId": {"pay-1"}, "param": {"ord-1"},
		"type": {"1"}, "price": {"8.00"}, "reallyPrice": {"8.00"},
	}
	values.Set("sign", md5Hex("pay-1ord-118.008.00secret"))
	mode, err := client.VerifyCallback(values)
	if err != nil || mode != "payId" {
		t.Fatalf("VerifyCallback() mode=%q err=%v", mode, err)
	}
}

func TestVerifyCallbackRejectsTampering(t *testing.T) {
	client := New(Config{MerchantID: "m1", Secret: "secret", CallbackSignMode: "auto"})
	values := url.Values{
		"mchId": {"m1"}, "orderId": {"cloud-1"}, "param": {"ord-1"},
		"type": {"2"}, "price": {"10.00"}, "reallyPrice": {"10.00"}, "sign": {"00000000000000000000000000000000"},
	}
	if _, err := client.VerifyCallback(values); err == nil {
		t.Fatal("tampered callback should fail")
	}
}
