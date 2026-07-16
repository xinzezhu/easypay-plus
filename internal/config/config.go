package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment          string
	HTTPAddr             string
	PublicBaseURL        string
	BasePath             string
	AdminToken           string
	MasterKey            string
	DBDriver             string
	MySQLDSN             string
	AutoMigrate          bool
	EpayMock             bool
	EpayBaseURL          string
	EpayMerchantID       string
	EpaySecret           string
	EpayCallbackSignMode string
	RequestMaxSkew       time.Duration
}

func Load() (Config, error) {
	cfg := Config{
		Environment:          env("APP_ENV", "development"),
		HTTPAddr:             env("HTTP_ADDR", ":8080"),
		PublicBaseURL:        strings.TrimRight(env("PUBLIC_BASE_URL", "http://localhost:8080"), "/"),
		BasePath:             normalizeBasePath(env("BASE_PATH", "")),
		AdminToken:           env("ADMIN_TOKEN", "dev-admin-token"),
		MasterKey:            env("APP_MASTER_KEY", "dev-only-master-key-change-before-production"),
		DBDriver:             env("DB_DRIVER", "mysql"),
		MySQLDSN:             env("MYSQL_DSN", "easypay:easypay@tcp(127.0.0.1:3306)/easypay_plus?parseTime=true&charset=utf8mb4&loc=Local&multiStatements=true"),
		AutoMigrate:          envBool("AUTO_MIGRATE", true),
		EpayMock:             envBool("EPAY_MOCK", true),
		EpayBaseURL:          strings.TrimRight(env("EPAY_BASE_URL", "https://epay.jylt.cc"), "/"),
		EpayMerchantID:       os.Getenv("EPAY_MCH_ID"),
		EpaySecret:           os.Getenv("EPAY_SECRET"),
		EpayCallbackSignMode: env("EPAY_CALLBACK_SIGN_MODE", "auto"),
		RequestMaxSkew:       5 * time.Minute,
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func normalizeBasePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return ""
	}
	return "/" + strings.Trim(value, "/")
}

func (c Config) Validate() error {
	if c.PublicBaseURL == "" || c.AdminToken == "" || c.MasterKey == "" {
		return errors.New("PUBLIC_BASE_URL, ADMIN_TOKEN and APP_MASTER_KEY are required")
	}
	if c.DBDriver != "mysql" && c.DBDriver != "memory" {
		return fmt.Errorf("unsupported DB_DRIVER %q", c.DBDriver)
	}
	if c.DBDriver == "mysql" && c.MySQLDSN == "" {
		return errors.New("MYSQL_DSN is required when DB_DRIVER=mysql")
	}
	if !c.EpayMock && (c.EpayMerchantID == "" || c.EpaySecret == "") {
		return errors.New("EPAY_MCH_ID and EPAY_SECRET are required when EPAY_MOCK=false")
	}
	if c.EpayCallbackSignMode != "auto" && c.EpayCallbackSignMode != "orderId" && c.EpayCallbackSignMode != "payId" {
		return errors.New("EPAY_CALLBACK_SIGN_MODE must be auto, orderId or payId")
	}
	if c.Environment == "production" {
		if c.AdminToken == "dev-admin-token" || strings.HasPrefix(c.MasterKey, "dev-only-") {
			return errors.New("development ADMIN_TOKEN or APP_MASTER_KEY cannot be used in production")
		}
		if c.DBDriver != "mysql" {
			return errors.New("production requires DB_DRIVER=mysql")
		}
	}
	return nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
