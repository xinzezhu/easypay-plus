package money

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

func Parse(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return 0, errors.New("amount must be a positive decimal")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts[0]) == 0 {
		return 0, errors.New("invalid amount")
	}
	if len(parts[0]) > 9 {
		return 0, errors.New("amount is too large")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, errors.New("invalid amount")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) > 2 {
			return 0, errors.New("amount supports at most two decimal places")
		}
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	cents := int64(0)
	if fraction != "" {
		cents, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, errors.New("invalid amount")
		}
	}
	total := whole*100 + cents
	if total <= 0 {
		return 0, errors.New("amount must be greater than zero")
	}
	return total, nil
}

func Format(cents int64) string {
	return fmt.Sprintf("%d.%02d", cents/100, cents%100)
}
