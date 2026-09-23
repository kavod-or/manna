package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	admincontent "manna/internal/admin"
	"manna/internal/menu"
	"manna/internal/version"
)

const maxBrandingLogoSize = 5 << 20

type server struct {
	events        EventsLoader
	page          *template.Template
	static        http.Handler
	adminStatic   http.Handler
	assetVersions map[string]string
	content       fs.FS
	admin         admincontent.Content
	logger        *slog.Logger
}

type EventsLoader func() (map[string]menu.Loader, error)

type AdminOptions struct {
	Password          string
	Content           admincontent.Content
	TrustProxyHTTPS   bool
	TrustedProxyCIDRs []netip.Prefix
}

type localizedView struct {
	Value     menu.Localized
	Languages []string
	Present   bool
}

type availabilityView struct {
	Item       menu.Item
	Conference menu.Conference
	Languages  []string
}

type regulatoryView struct {
	Name       menu.Localized
	Regulatory menu.Regulatory
	Conference menu.Conference
}

type variantsView struct {
	Variants   []menu.Localized
	Conference menu.Conference
}

type statusView struct {
	SoldOut    bool
	Conference menu.Conference
}

type priceView struct {
	Price     *menu.Price
	Languages []string
	Currency  menu.Currency
}

type priceTableView struct {
	Items       []menu.Item
	Conference  menu.Conference
	SectionKey  string
	HasSmall    bool
	HasNormal   bool
	HasLarge    bool
	SizeColumns int
}

type responsivePriceTablesView struct {
	Compact priceTableView
	Wide    []priceTableView
}

var interfaceText = map[string]menu.Localized{
	"meals":        interfaceTranslation("Essen", "Meals", "Еда"),
	"refreshments": interfaceTranslation("Getränke & Snacks", "Drinks & Snacks", "Напитки и снеки"),
	"schedule":     interfaceTranslation("Tagesplan", "Schedule", "Расписание"),
	"food_trucks":  interfaceTranslation("Food Trucks", "Food Trucks", "Фудтраки — уличная еда"),
	"more":         interfaceTranslation("Zusätzlich vor Ort", "More to enjoy", "Дополнительные угощения"),
	"location":     interfaceTranslation("Standort", "Location", "Локация"),
	"all_day":      interfaceTranslation("Durchgehend", "All day", "Весь день"),
	"coffee":       interfaceTranslation("Kaffee", "Coffee", "Кофе"),
	"drinks":       interfaceTranslation("Getränke", "Drinks", "Напитки"),
	"snacks":       interfaceTranslation("Snacks", "Snacks", "Снеки"),
	"program":      interfaceTranslation("Programm", "Schedule", "Программа"),
	"full_day":     interfaceTranslation("Der ganze Tag", "The full day", "Меню на весь день"),
	"enjoy":        interfaceTranslation("Guten Appetit!", "Enjoy your meal!", "Приятного аппетита!"),
	"sold_out":     interfaceTranslation("Ausverkauft", "Sold out", "Распродано"),
	"price":        interfaceTranslation("Preis", "Price", "Цена"),
	"small":        interfaceTranslation("Klein", "Small", "Маленький"),
	"regular":      interfaceTranslation("Normal", "Regular", "Обычный"),
	"large":        interfaceTranslation("Groß", "Large", "Большой"),
	"food_info":    interfaceTranslation("Allergene & Lebensmittelinfos", "Allergens & food information", "Аллергены и информация о продукте"),
	"allergens":    interfaceTranslation("Allergene", "Allergens", "Аллергены"),
	"additives":    interfaceTranslation("Zusatzstoffe", "Additives", "Пищевые добавки"),
	"notices":      interfaceTranslation("Weitere Hinweise", "Other notices", "Другие сведения"),
}

var publicStaticAssets = map[string]struct{}{
	"hearts.js": {}, "girly.css": {}, "mazel-tov.js": {}, "mazel-tov.css": {},
	"mazel-tov-glass.png": {}, "manna.js": {}, "app.js": {}, "styles.css": {},
	"logo.png": {}, "favicon.png": {},
}

var adminStaticAssets = map[string]struct{}{
	"admin.css": {}, "admin.js": {},
}

func interfaceTranslation(de, en, ru string) menu.Localized {
	return menu.Localized{DE: de, EN: en, Other: map[string]string{"ru": ru}}
}

func newPriceTableView(items []menu.Item, conference menu.Conference, sectionKey string) priceTableView {
	view := priceTableView{Items: items, Conference: conference, SectionKey: sectionKey}
	if !conference.HidePrices {
		for _, item := range items {
			view.HasSmall = view.HasSmall || item.PriceSmall != nil
			view.HasNormal = view.HasNormal || item.PriceNormal != nil
			view.HasLarge = view.HasLarge || item.PriceLarge != nil
		}
	}
	for _, present := range []bool{view.HasSmall, view.HasNormal, view.HasLarge} {
		if present {
			view.SizeColumns++
		}
	}
	if view.SizeColumns == 0 {
		view.SizeColumns = 1
	}
	return view
}

func splitPriceTableViews(items []menu.Item, conference menu.Conference, sectionKey string) []priceTableView {
	if len(items) == 0 {
		return nil
	}
	middle := (len(items) + 1) / 2
	tables := []priceTableView{newPriceTableView(items[:middle], conference, sectionKey)}
	if middle < len(items) {
		tables = append(tables, newPriceTableView(items[middle:], conference, sectionKey))
	}
	return tables
}

func newResponsivePriceTablesView(items []menu.Item, conference menu.Conference, sectionKey string) responsivePriceTablesView {
	return responsivePriceTablesView{
		Compact: newPriceTableView(items, conference, sectionKey),
		Wide:    splitPriceTableViews(items, conference, sectionKey),
	}
}

func New(events map[string]menu.Loader, templates fs.FS, static fs.FS, logger *slog.Logger) (http.Handler, error) {
	return NewDynamic(func() (map[string]menu.Loader, error) { return events, nil }, templates, static, nil, logger)
}

func NewDynamic(events EventsLoader, templates fs.FS, static fs.FS, content fs.FS, logger *slog.Logger) (http.Handler, error) {
	return NewDynamicWithAdmin(events, templates, static, content, logger, AdminOptions{})
}

func NewDynamicWithAdmin(events EventsLoader, templates fs.FS, static fs.FS, content fs.FS, logger *slog.Logger, adminOptions AdminOptions) (http.Handler, error) {
	assetVersions := fingerprintStaticAssets(static)
	functions := template.FuncMap{
		"version": func() string { return version.Current },
		"assetURL": func(name string) string {
			return versionedAssetURL("/static/", name, assetVersions)
		},
		"adminAssetURL": func(name string) string {
			return versionedAssetURL("/admin/static/", name, assetVersions)
		},
		"languages": func(conference menu.Conference) []string { return conference.LanguageCodes() },
		"primary":   func(conference menu.Conference) string { return conference.LanguageCodes()[0] },
		"direction": func(language string) string {
			switch strings.SplitN(language, "-", 2)[0] {
			case "ar", "fa", "he", "ur":
				return "rtl"
			default:
				return "ltr"
			}
		},
		"upper": strings.ToUpper,
		"text":  func(value menu.Localized, language string) string { return value.Text(language) },
		"localize": func(value menu.Localized, conference menu.Conference) localizedView {
			return localizedView{Value: value, Languages: conference.LanguageCodes(), Present: !value.Empty()}
		},
		"ui": func(key string, conference menu.Conference) localizedView {
			value := interfaceText[key]
			return localizedView{Value: value, Languages: conference.LanguageCodes(), Present: true}
		},
		"availability": func(item menu.Item, conference menu.Conference) availabilityView {
			return availabilityView{Item: item, Conference: conference, Languages: conference.LanguageCodes()}
		},
		"regulatoryView": func(item menu.RegulatoryItem, conference menu.Conference) regulatoryView {
			return regulatoryView{Name: item.Name, Regulatory: item.Regulatory, Conference: conference}
		},
		"variants": func(item menu.Item, conference menu.Conference) variantsView {
			return variantsView{Variants: item.Variants, Conference: conference}
		},
		"status": func(soldOut bool, conference menu.Conference) statusView {
			return statusView{SoldOut: soldOut, Conference: conference}
		},
		"priceView": func(price *menu.Price, conference menu.Conference) priceView {
			return priceView{Price: price, Languages: conference.LanguageCodes(), Currency: conference.EffectiveCurrency()}
		},
		"formatPrice": func(price *menu.Price, language string, currency menu.Currency) string {
			if price == nil {
				return ""
			}
			return price.Localized(language, currency)
		},
		"priceTables": newResponsivePriceTablesView,
		"tag": func(tags map[string]menu.Localized, id, language string) string {
			value, ok := tags[id]
			if !ok {
				return id
			}
			return value.Text(language)
		},
	}

	page, err := template.New("index.html").Funcs(functions).ParseFS(templates, "web/templates/index.html")
	if err != nil {
		return nil, err
	}

	if _, err := page.New("landing").Parse(landingPage); err != nil {
		return nil, err
	}
	if _, err := page.New("not-found").Parse(notFoundPage); err != nil {
		return nil, err
	}
	if adminOptions.Password != "" && adminOptions.Content != nil {
		if _, err := page.ParseFS(templates, "web/templates/admin.html"); err != nil {
			return nil, err
		}
	}
	s := &server{
		events:        events,
		page:          page,
		static:        http.StripPrefix("/static/", http.FileServer(http.FS(static))),
		adminStatic:   http.StripPrefix("/admin/static/", http.FileServer(http.FS(static))),
		assetVersions: assetVersions,
		content:       content,
		admin:         adminOptions.Content,
		logger:        logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.index)
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /static/", s.asset)
	mux.HandleFunc("GET /branding/", s.brandingLogo)
	var handler http.Handler = mux
	if adminOptions.Password != "" {
		securedAdmin := adminSecurity(adminOptions.Password, adminOptions.TrustProxyHTTPS, adminOptions.TrustedProxyCIDRs, s.adminHandler())
		handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/admin" || strings.HasPrefix(request.URL.Path, "/admin/") {
				securedAdmin.ServeHTTP(writer, request)
				return
			}
			mux.ServeHTTP(writer, request)
		})
	}
	return securityHeaders(gzipResponses(accessLog(handler, logger))), nil
}

func (s *server) brandingLogo(writer http.ResponseWriter, request *http.Request) {
	if s.content == nil {
		http.NotFound(writer, request)
		return
	}
	eventPath := strings.TrimPrefix(request.URL.Path, "/branding")
	events, err := s.events()
	if err != nil {
		s.logger.Warn("event manifest reload failed; serving last valid version", "error", err)
	}
	loader, ok := events[eventPath]
	if !ok {
		http.NotFound(writer, request)
		return
	}
	config, err := loader()
	if err != nil {
		s.logger.Warn("menu reload failed; serving last valid version", "event", eventPath, "error", err)
	}
	if config.Conference.Logo == "" {
		http.NotFound(writer, request)
		return
	}
	info, err := fs.Stat(s.content, config.Conference.Logo)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxBrandingLogoSize {
		http.NotFound(writer, request)
		return
	}
	file, err := s.content.Open(config.Conference.Logo)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	defer file.Close()
	logo, err := io.ReadAll(io.LimitReader(file, maxBrandingLogoSize+1))
	if err != nil || len(logo) > maxBrandingLogoSize {
		http.NotFound(writer, request)
		return
	}
	contentType := http.DetectContentType(logo)
	if !strings.HasPrefix(contentType, "image/") {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Cache-Control", "no-cache")
	writer.Header().Set("Content-Type", contentType)
	http.ServeContent(writer, request, "branding-logo", info.ModTime(), bytes.NewReader(logo))
}

// Only shared presentation assets are public. Never expose a directory listing,
// source maps, or configuration files accidentally placed in the static folder.
func (s *server) asset(writer http.ResponseWriter, request *http.Request) {
	name := strings.TrimPrefix(request.URL.Path, "/static/")
	if _, ok := publicStaticAssets[name]; !ok {
		writer.Header().Set("Cache-Control", "no-store")
		http.NotFound(writer, request)
		return
	}
	if version := s.assetVersions[name]; version != "" && !hasCanonicalAssetVersion(request, version) {
		writer.Header().Set("Cache-Control", "no-store")
		http.Redirect(writer, request, versionedAssetURL("/static/", name, s.assetVersions), http.StatusTemporaryRedirect)
		return
	}
	cacheStatic(s.static).ServeHTTP(writer, request)
}

func fingerprintStaticAssets(static fs.FS) map[string]string {
	versions := make(map[string]string, len(publicStaticAssets)+len(adminStaticAssets))
	for _, allowed := range []map[string]struct{}{publicStaticAssets, adminStaticAssets} {
		for name := range allowed {
			content, err := fs.ReadFile(static, name)
			if err != nil {
				continue
			}
			digest := sha256.Sum256(content)
			versions[name] = hex.EncodeToString(digest[:])
		}
	}
	return versions
}

func versionedAssetURL(prefix, name string, versions map[string]string) string {
	url := prefix + name
	if version := versions[name]; version != "" {
		return url + "?v=" + version
	}
	return url
}

func hasCanonicalAssetVersion(request *http.Request, version string) bool {
	query := request.URL.Query()
	values, ok := query["v"]
	return ok && len(query) == 1 && len(values) == 1 && values[0] == version
}

func (s *server) index(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	if request.URL.Path == "/" {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = s.page.ExecuteTemplate(writer, "landing", nil)
		return
	}
	events, reloadErr := s.events()
	if reloadErr != nil {
		s.logger.Warn("event manifest reload failed; serving last valid version", "error", reloadErr)
	}
	loader, ok := events[request.URL.Path]
	if !ok {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.WriteHeader(http.StatusNotFound)
		_ = s.page.ExecuteTemplate(writer, "not-found", nil)
		return
	}
	config, err := loader()
	if err != nil {
		s.logger.Warn("menu reload failed; serving last valid version", "event", request.URL.Path, "error", err)
	}
	data := struct {
		menu.Config
		EventPath string
	}{config, request.URL.Path}
	var body bytes.Buffer
	if err := s.page.ExecuteTemplate(&body, "index.html", data); err != nil {
		s.logger.Error("render page", "error", err)
		http.Error(writer, "Could not render menu", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = writer.Write(body.Bytes())
}

func (s *server) health(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte(`{"status":"ok"}`))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; font-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(writer, request)
	})
}

func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(writer, request)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	writer io.Writer
}

func (writer gzipResponseWriter) WriteHeader(status int) {
	writer.Header().Del("Content-Length")
	writer.ResponseWriter.WriteHeader(status)
}

func (writer gzipResponseWriter) Write(content []byte) (int, error) {
	writer.Header().Del("Content-Length")
	return writer.writer.Write(content)
}

func gzipResponses(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		compressible := !strings.HasPrefix(request.URL.Path, "/static/") || strings.HasSuffix(request.URL.Path, ".css") || strings.HasSuffix(request.URL.Path, ".js")
		if compressible {
			writer.Header().Add("Vary", "Accept-Encoding")
		}
		if request.Method == http.MethodHead || !acceptsGzip(request.Header.Get("Accept-Encoding")) || !compressible || request.Header.Get("Range") != "" {
			next.ServeHTTP(writer, request)
			return
		}

		compressed, err := gzip.NewWriterLevel(writer, gzip.BestSpeed)
		if err != nil {
			next.ServeHTTP(writer, request)
			return
		}
		defer compressed.Close()

		writer.Header().Set("Content-Encoding", "gzip")
		next.ServeHTTP(gzipResponseWriter{ResponseWriter: writer, writer: compressed}, request)
	})
}

func acceptsGzip(header string) bool {
	wildcard := false
	for _, encoding := range strings.Split(header, ",") {
		parts := strings.Split(encoding, ";")
		name := strings.TrimSpace(parts[0])
		quality := 1.0
		for _, parameter := range parts[1:] {
			key, value, ok := strings.Cut(parameter, "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "q") {
				parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
				quality = 0
				if err == nil && parsed >= 0 && parsed <= 1 {
					quality = parsed
				}
			}
		}
		if strings.EqualFold(name, "gzip") {
			return quality > 0
		}
		if name == "*" {
			wildcard = quality > 0
		}
	}
	return wildcard
}

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *responseRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func accessLog(next http.Handler, logger *slog.Logger) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: writer, status: http.StatusOK}
		next.ServeHTTP(recorder, request)
		// URL.Path contains the endpoint but excludes query parameters, user info,
		// headers (including Authorization), and the request body.
		logger.Info("request", "method", request.Method, "path", request.URL.Path, "status", recorder.status, "duration", time.Since(started))
	})
}
