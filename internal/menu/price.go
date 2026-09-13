package menu

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Price stores the smallest currency unit exactly, without floating-point rounding.
type Price int64

type Currency string

const (
	Euro    Currency = "euro"
	Dollar  Currency = "dollar"
	Schekel Currency = "schekel"
)

var pricePattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,2})?$`)

func (price *Price) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || !pricePattern.MatchString(node.Value) {
		return fmt.Errorf("line %d: price must be a non-negative amount with at most two decimal places", node.Line)
	}
	parts := strings.SplitN(node.Value, ".", 2)
	fraction := "00"
	if len(parts) == 2 {
		fraction = (parts[1] + "0")[:2]
	}
	cents, err := strconv.ParseInt(parts[0]+fraction, 10, 64)
	if err != nil {
		return fmt.Errorf("line %d: price is too large", node.Line)
	}
	*price = Price(cents)
	return nil
}

func (price Price) German() string {
	return price.localized("de", Euro)
}

func (price Price) English() string {
	return price.localized("en", Euro)
}

func (price Price) Localized(language string, configured ...Currency) string {
	currency := Euro
	if len(configured) > 0 && configured[0] != "" {
		currency = configured[0]
	}
	return price.localized(language, currency)
}

func (price Price) localized(language string, currency Currency) string {
	symbol := currency.Symbol()
	switch strings.SplitN(language, "-", 2)[0] {
	case "fr":
		return price.format("\u202f", ",") + "\u00a0" + symbol
	case "ru":
		return price.format("\u00a0", ",") + "\u00a0" + symbol
	case "de", "es", "it", "nl", "pt":
		return price.format(".", ",") + "\u00a0" + symbol
	default:
		return symbol + price.format(",", ".")
	}
}

func (currency Currency) Symbol() string {
	switch currency {
	case Dollar:
		return "$"
	case Schekel:
		return "₪"
	default:
		return "€"
	}
}

func (price Price) format(group, decimal string) string {
	whole := strconv.FormatInt(int64(price)/100, 10)
	for index := len(whole) - 3; index > 0; index -= 3 {
		whole = whole[:index] + group + whole[index:]
	}
	return fmt.Sprintf("%s%s%02d", whole, decimal, price%100)
}
