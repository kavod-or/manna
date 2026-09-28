package menu

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"path"
	"reflect"
	"regexp"
	"strings"
	"time"
	_ "time/tzdata"

	"gopkg.in/yaml.v3"
)

type Localized struct {
	DE    string            `yaml:"de"`
	EN    string            `yaml:"en"`
	Other map[string]string `yaml:",inline"`
}

// MarshalJSON keeps translations as a flat object, matching their YAML shape.
// The admin UI can therefore handle every configured language without knowing
// about the Go representation used for the two most common languages.
func (localized Localized) MarshalJSON() ([]byte, error) {
	translations := make(map[string]string, len(localized.Other)+2)
	if localized.DE != "" {
		translations["de"] = localized.DE
	}
	if localized.EN != "" {
		translations["en"] = localized.EN
	}
	for language, value := range localized.Other {
		translations[language] = value
	}
	return json.Marshal(translations)
}

func (localized *Localized) UnmarshalJSON(data []byte) error {
	var translations map[string]string
	if err := json.Unmarshal(data, &translations); err != nil {
		return err
	}
	localized.DE = translations["de"]
	localized.EN = translations["en"]
	delete(translations, "de")
	delete(translations, "en")
	localized.Other = translations
	return nil
}

type Config struct {
	Conference Conference            `yaml:"conference" json:"conference"`
	Tags       map[string]Localized  `yaml:"tags,omitempty" json:"tags"`
	Regulatory map[string]Regulatory `yaml:"regulatory,omitempty" json:"regulatory"`
	Permanent  Permanent             `yaml:"permanent" json:"permanent"`
	Days       []Day                 `yaml:"days" json:"days"`
}

type Conference struct {
	Currency      Currency  `yaml:"currency,omitempty" json:"currency"`
	HidePrices    bool      `yaml:"hide_prices,omitempty" json:"hide_prices"`
	Payment       Localized `yaml:"payment,omitempty" json:"payment"`
	Languages     []string  `yaml:"languages" json:"languages"`
	SeriousMode   bool      `yaml:"serious_mode,omitempty" json:"serious_mode"`
	EasterEggMode string    `yaml:"easter_egg_mode,omitempty" json:"easter_egg_mode"`
	TimeZone      string    `yaml:"timezone" json:"timezone"`
	Logo          string    `yaml:"logo,omitempty" json:"logo"`
	Name          Localized `yaml:"name" json:"name"`
	Location      Localized `yaml:"location" json:"location"`
}

func (conference Conference) EffectiveCurrency() Currency {
	if conference.Currency == "" {
		return Euro
	}
	return conference.Currency
}

type Permanent struct {
	Coffee []Item `yaml:"coffee,omitempty" json:"coffee"`
	Drinks []Item `yaml:"drinks,omitempty" json:"drinks"`
	Snacks []Item `yaml:"snacks,omitempty" json:"snacks"`
}

type Day struct {
	FoodTrucks []FoodTruck `yaml:"food_trucks,omitempty" json:"food_trucks"`
	Date       string      `yaml:"date" json:"date"`
	Services   []Service   `yaml:"services" json:"services"`
}

type FoodTruck struct {
	Items       []Item       `yaml:"items,omitempty" json:"items"`
	Times       []TimeWindow `yaml:"times,omitempty" json:"times"`
	Payment     Localized    `yaml:"payment,omitempty" json:"payment"`
	ID          string       `yaml:"id" json:"id"`
	Name        Localized    `yaml:"name" json:"name"`
	Description Localized    `yaml:"description" json:"description"`
	Location    Localized    `yaml:"location" json:"location"`
	From        string       `yaml:"from,omitempty" json:"from"`
	Until       string       `yaml:"until,omitempty" json:"until"`
}

type TimeWindow struct {
	From  string `yaml:"from" json:"from"`
	Until string `yaml:"until" json:"until"`
}

type Service struct {
	SoldOut     bool      `yaml:"sold_out,omitempty" json:"sold_out"`
	PriceSmall  *Price    `yaml:"price_small,omitempty" json:"price_small,omitempty"`   // Accepted for compatibility; service prices are not displayed.
	PriceNormal *Price    `yaml:"price_normal,omitempty" json:"price_normal,omitempty"` // Accepted for compatibility; service prices are not displayed.
	PriceLarge  *Price    `yaml:"price_large,omitempty" json:"price_large,omitempty"`   // Accepted for compatibility; service prices are not displayed.
	Price       *Price    `yaml:"price,omitempty" json:"price,omitempty"`               // Accepted for compatibility; service prices are not displayed.
	ID          string    `yaml:"id" json:"id"`
	Title       Localized `yaml:"title" json:"title"`
	Subtitle    Localized `yaml:"subtitle" json:"subtitle"`
	From        string    `yaml:"from" json:"from"`
	Until       string    `yaml:"until" json:"until"`
	Items       []Item    `yaml:"items,omitempty" json:"items"`
}

type Item struct {
	SoldOut     bool        `yaml:"sold_out,omitempty" json:"sold_out"`
	PriceSmall  *Price      `yaml:"price_small,omitempty" json:"price_small,omitempty"`
	PriceNormal *Price      `yaml:"price_normal,omitempty" json:"price_normal,omitempty"`
	PriceLarge  *Price      `yaml:"price_large,omitempty" json:"price_large,omitempty"`
	Price       *Price      `yaml:"price,omitempty" json:"price,omitempty"`
	ID          string      `yaml:"id" json:"id"`
	Name        Localized   `yaml:"name" json:"name"`
	Description Localized   `yaml:"description,omitempty" json:"description"`
	Variants    []Localized `yaml:"variants,omitempty" json:"variants"`
	Tags        []string    `yaml:"tags,omitempty" json:"tags"`
}

// Regulatory holds item-specific declarations shown before a guest orders.
type Regulatory struct {
	Allergens []Localized `yaml:"allergens,omitempty" json:"allergens"`
	Additives []Localized `yaml:"additives,omitempty" json:"additives"`
	Notices   []Localized `yaml:"notices,omitempty" json:"notices"`
}

func (regulatory Regulatory) Present() bool {
	return len(regulatory.Allergens)+len(regulatory.Additives)+len(regulatory.Notices) > 0
}

type RegulatoryItem struct {
	Name       Localized
	Regulatory Regulatory
}

func (config Config) HasRegulatory() bool {
	return len(config.RegulatoryItems()) > 0
}

// RegulatoryItems returns each distinct name and declaration once for the footer.
func (config Config) RegulatoryItems() []RegulatoryItem {
	var items []RegulatoryItem
	add := func(item Item) {
		regulatory, ok := config.Regulatory[item.ID]
		if !ok || !regulatory.Present() {
			return
		}
		for _, existing := range items {
			if reflect.DeepEqual(existing.Name, item.Name) && reflect.DeepEqual(existing.Regulatory, regulatory) {
				return
			}
		}
		items = append(items, RegulatoryItem{Name: item.Name, Regulatory: regulatory})
	}
	for _, day := range config.Days {
		for _, truck := range day.FoodTrucks {
			for _, item := range truck.Items {
				add(item)
			}
		}
		for _, service := range day.Services {
			for _, item := range service.Items {
				add(item)
			}
		}
	}
	for _, group := range [][]Item{config.Permanent.Coffee, config.Permanent.Drinks, config.Permanent.Snacks} {
		for _, item := range group {
			add(item)
		}
	}
	return items
}

func Decode(reader io.Reader) (Config, error) {
	decoder := yaml.NewDecoder(reader)
	decoder.KnownFields(true)

	var config Config
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode menu YAML: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return Config{}, fmt.Errorf("decode trailing menu YAML: %w", err)
		}
		return Config{}, fmt.Errorf("menu must contain exactly one YAML document")
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config Config) Validate() error {
	if err := validateCurrency(config.Conference.Currency); err != nil {
		return err
	}
	if _, err := time.LoadLocation(config.Conference.Zone()); err != nil {
		return fmt.Errorf("conference.timezone: %w", err)
	}

	languages, err := validateLanguages(config.Conference.Languages)
	if err != nil {
		return err
	}
	itemIDs := make(map[string]bool)

	if err := validateLocalized("conference.name", config.Conference.Name, languages); err != nil {
		return err
	}
	if err := validateLocalized("conference.location", config.Conference.Location, languages); err != nil {
		return err
	}
	if err := validatePayment("conference.payment", config.Conference.Payment, languages); err != nil {
		return err
	}
	if err := validateLogo(config.Conference.Logo); err != nil {
		return err
	}
	if err := validateEasterEggMode(config.Conference.EasterEggMode); err != nil {
		return err
	}
	for id, label := range config.Tags {
		if id == "" {
			return fmt.Errorf("tag ID is required")
		}
		if err := validateLocalized(fmt.Sprintf("tags.%s", id), label, languages); err != nil {
			return err
		}
	}
	for index, item := range config.Permanent.Coffee {
		itemIDs[item.ID] = true
		if err := config.validateItem(fmt.Sprintf("permanent.coffee[%d]", index), item); err != nil {
			return err
		}
	}
	for index, item := range config.Permanent.Drinks {
		itemIDs[item.ID] = true
		if err := config.validateItem(fmt.Sprintf("permanent.drinks[%d]", index), item); err != nil {
			return err
		}
	}
	for index, item := range config.Permanent.Snacks {
		itemIDs[item.ID] = true
		if err := config.validateItem(fmt.Sprintf("permanent.snacks[%d]", index), item); err != nil {
			return err
		}
	}
	if len(config.Days) == 0 {
		return fmt.Errorf("menu must contain at least one day")
	}

	seenDays := make(map[string]bool, len(config.Days))
	for dayIndex, day := range config.Days {
		if _, err := time.Parse("2006-01-02", day.Date); err != nil {
			return fmt.Errorf("days[%d].date must use YYYY-MM-DD", dayIndex)
		}
		if seenDays[day.Date] {
			return fmt.Errorf("day %q occurs more than once", day.Date)
		}
		seenDays[day.Date] = true
		if len(day.Services) == 0 {
			return fmt.Errorf("days[%d] must contain at least one service", dayIndex)
		}

		seenTrucks := make(map[string]bool)
		for truckIndex, truck := range day.FoodTrucks {
			path := fmt.Sprintf("days[%d].food_trucks[%d]", dayIndex, truckIndex)
			if truck.ID == "" || seenTrucks[truck.ID] {
				return fmt.Errorf("%s.id must be non-empty and unique within the day", path)
			}
			seenTrucks[truck.ID] = true
			for itemIndex, item := range truck.Items {
				itemIDs[item.ID] = true
				if err := config.validateItem(fmt.Sprintf("%s.items[%d]", path, itemIndex), item); err != nil {
					return err
				}
			}
			if err := validatePayment(path+".payment", truck.Payment, languages); err != nil {
				return err
			}
			for _, field := range []struct {
				name  string
				value Localized
			}{{"name", truck.Name}, {"description", truck.Description}, {"location", truck.Location}} {
				if err := validateLocalized(path+"."+field.name, field.value, languages); err != nil {
					return err
				}
			}
			if len(truck.Times) > 0 {
				if truck.From != "" || truck.Until != "" {
					return fmt.Errorf("%s: use either times or from/until", path)
				}
				for timeIndex, window := range truck.Times {
					if err := validateTimeWindow(fmt.Sprintf("%s.times[%d]", path, timeIndex), window.From, window.Until); err != nil {
						return err
					}
				}
			} else if err := validateTimeWindow(path, truck.From, truck.Until); err != nil {
				return err
			}
		}
		for serviceIndex, service := range day.Services {
			path := fmt.Sprintf("days[%d].services[%d]", dayIndex, serviceIndex)
			if err := validatePrices(path, service.Price, service.PriceSmall, service.PriceNormal, service.PriceLarge); err != nil {
				return err
			}
			if service.ID == "" {
				return fmt.Errorf("%s.id is required", path)
			}
			if err := validateLocalized(path+".title", service.Title, languages); err != nil {
				return err
			}
			if err := validateLocalized(path+".subtitle", service.Subtitle, languages); err != nil {
				return err
			}
			if err := validateTime(path+".from", service.From); err != nil {
				return err
			}
			if err := validateTime(path+".until", service.Until); err != nil {
				return err
			}
			if service.Until <= service.From {
				return fmt.Errorf("%s.until must be after from on the same day", path)
			}
			for itemIndex, item := range service.Items {
				itemIDs[item.ID] = true
				if err := config.validateItem(fmt.Sprintf("%s.items[%d]", path, itemIndex), item); err != nil {
					return err
				}
			}
		}
	}
	for id, regulatory := range config.Regulatory {
		regulatoryPath := fmt.Sprintf("regulatory.%s", id)
		if id == "" || !itemIDs[id] {
			return fmt.Errorf("%s must refer to an existing item ID", regulatoryPath)
		}
		if !regulatory.Present() {
			return fmt.Errorf("%s must contain allergens, additives, or notices", regulatoryPath)
		}
		for _, group := range []struct {
			name   string
			values []Localized
		}{{"allergens", regulatory.Allergens}, {"additives", regulatory.Additives}, {"notices", regulatory.Notices}} {
			for index, value := range group.values {
				if err := validateLocalized(fmt.Sprintf("%s.%s[%d]", regulatoryPath, group.name, index), value, languages); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func validateCurrency(currency Currency) error {
	switch currency {
	case "", Euro, Dollar, Schekel:
		return nil
	default:
		return fmt.Errorf("conference.currency must be euro, dollar, or schekel")
	}
}

func validateLogo(filename string) error {
	if filename == "" {
		return nil
	}
	if !fs.ValidPath(filename) || strings.Contains(filename, `\`) {
		return fmt.Errorf("conference.logo must be a relative file path")
	}
	switch strings.ToLower(path.Ext(filename)) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".avif":
		return nil
	default:
		return fmt.Errorf("conference.logo must be a PNG, JPEG, WebP, GIF, or AVIF image")
	}
}

func (config Config) validateItem(path string, item Item) error {
	if err := validatePrices(path, item.Price, item.PriceSmall, item.PriceNormal, item.PriceLarge); err != nil {
		return err
	}
	if item.ID == "" {
		return fmt.Errorf("%s.id is required", path)
	}
	languages := config.Conference.LanguageCodes()
	if err := validateLocalized(path+".name", item.Name, languages); err != nil {
		return err
	}
	if err := validateOptionalLocalized(path+".description", item.Description, languages); err != nil {
		return err
	}
	for index, variant := range item.Variants {
		if err := validateLocalized(fmt.Sprintf("%s.variants[%d]", path, index), variant, languages); err != nil {
			return err
		}
	}
	for _, tag := range item.Tags {
		if _, ok := config.Tags[tag]; !ok {
			return fmt.Errorf("%s references unknown tag %q", path, tag)
		}
	}
	return nil
}

func validatePayment(path string, value Localized, languages []string) error {
	if value.Empty() {
		return nil
	}
	return validateLocalized(path, value, languages)
}

func validateLocalized(path string, value Localized, languages []string) error {
	for _, language := range languages {
		if strings.TrimSpace(value.Exact(language)) == "" {
			return fmt.Errorf("%s requires a %s translation", path, language)
		}
	}
	return nil
}

func validateOptionalLocalized(path string, value Localized, languages []string) error {
	if value.Empty() {
		return nil
	}
	return validateLocalized(path, value, languages)
}

func validateTime(path, value string) error {
	if parsed, err := time.Parse("15:04", value); err != nil || parsed.Format("15:04") != value {
		return fmt.Errorf("%s must use HH:MM", path)
	}
	return nil
}

func validateTimeWindow(path, from, until string) error {
	if err := validateTime(path+".from", from); err != nil {
		return err
	}
	if err := validateTime(path+".until", until); err != nil {
		return err
	}
	if until <= from {
		return fmt.Errorf("%s.until must be after from on the same day", path)
	}
	return nil
}

func validatePrices(path string, single, small, normal, large *Price) error {
	if single != nil && (small != nil || normal != nil || large != nil) {
		return fmt.Errorf("%s: use either price or price_small/price_normal/price_large", path)
	}
	return nil
}

func (conference Conference) Zone() string {
	if conference.TimeZone == "" {
		return "Europe/Berlin"
	}
	return conference.TimeZone
}

func (conference Conference) EffectiveEasterEggMode() string {
	if conference.SeriousMode {
		return "none"
	}
	if conference.EasterEggMode == "" {
		return "girly_vibes"
	}
	return conference.EasterEggMode
}

func validateEasterEggMode(mode string) error {
	switch mode {
	case "", "girly_vibes", "mazel_tov":
		return nil
	default:
		return fmt.Errorf("conference.easter_egg_mode must be girly_vibes or mazel_tov")
	}
}

var languagePattern = regexp.MustCompile(`^[a-z]{2,3}(?:-[a-z0-9]{2,8})*$`)

func validateLanguages(configured []string) ([]string, error) {
	languages := configured
	if len(languages) == 0 {
		languages = []string{"de", "en"}
	}
	seen := make(map[string]bool, len(languages))
	for _, language := range languages {
		if !languagePattern.MatchString(language) {
			return nil, fmt.Errorf("conference.languages contains invalid language code %q", language)
		}
		if seen[language] {
			return nil, fmt.Errorf("conference.languages contains duplicate language code %q", language)
		}
		seen[language] = true
	}
	return languages, nil
}

func (conference Conference) LanguageCodes() []string {
	if len(conference.Languages) == 0 {
		return []string{"de", "en"}
	}
	return conference.Languages
}

func (localized Localized) Exact(language string) string {
	switch language {
	case "de":
		return localized.DE
	case "en":
		return localized.EN
	default:
		return localized.Other[language]
	}
}

func (localized Localized) Text(language string) string {
	if value := localized.Exact(language); value != "" {
		return value
	}
	if primary := strings.SplitN(language, "-", 2)[0]; primary != language {
		if value := localized.Exact(primary); value != "" {
			return value
		}
	}
	if localized.EN != "" {
		return localized.EN
	}
	return localized.DE
}

func (localized Localized) Empty() bool {
	if localized.DE != "" || localized.EN != "" {
		return false
	}
	for _, value := range localized.Other {
		if value != "" {
			return false
		}
	}
	return true
}
