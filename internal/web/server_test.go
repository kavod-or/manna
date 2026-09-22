package web

import (
	"bytes"
	"compress/gzip"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"manna/internal/menu"
)

func TestServerRendersMenuAndSecurityHeaders(t *testing.T) {
	templateFS := fstest.MapFS{
		"web/templates/index.html": {Data: []byte(`{{define "index.html"}}<h1>{{.Conference.Name.DE}}</h1>{{end}}`)},
	}
	staticFS := fstest.MapFS{"styles.css": {Data: []byte("body{}")}}
	config := menu.Config{
		Conference: menu.Conference{Name: menu.Localized{DE: "Manna Konferenz", EN: "Manna Conference"}, Location: menu.Localized{DE: "Foyer", EN: "Foyer"}},
		Days:       []menu.Day{{Date: "2026-10-12", Services: []menu.Service{{ID: "lunch", Title: menu.Localized{DE: "Mittagessen", EN: "Lunch"}, From: "12:00", Until: "13:00"}}}},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	handler, err := newTestServer(func() (menu.Config, error) { return config, nil }, templateFS, staticFS, logger)
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/test", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if !strings.Contains(response.Body.String(), "Manna Konferenz") {
		t.Fatalf("response does not contain conference name: %s", response.Body.String())
	}
	if got := response.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'self'") {
		t.Fatalf("unexpected CSP: %q", got)
	}
}

func TestMenuContentCannotInjectHTMLOrScript(t *testing.T) {
	malicious := `<img src=x onerror="alert(1)"><script>alert(2)</script>`
	config := menu.Config{
		Conference: menu.Conference{
			Name:     menu.Localized{DE: malicious, EN: malicious},
			Location: menu.Localized{DE: malicious, EN: malicious},
		},
		Days: []menu.Day{{
			Date: "2026-10-12",
			Services: []menu.Service{{
				ID:       "lunch",
				Title:    menu.Localized{DE: malicious, EN: malicious},
				Subtitle: menu.Localized{DE: malicious, EN: malicious},
				From:     "12:00",
				Until:    "13:00",
			}},
		}},
	}
	handler, err := New(map[string]menu.Loader{"/event": func() (menu.Config, error) { return config, nil }}, os.DirFS("../.."), os.DirFS("../../web/static"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/event", nil))
	body := response.Body.String()
	if strings.Contains(body, "<script>alert") || strings.Contains(body, "<img src=x") || strings.Contains(body, `onerror="alert`) {
		t.Fatalf("menu content rendered as executable markup: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("malicious menu content was not visibly HTML-escaped")
	}
}

func TestAccessLogIncludesAdminEndpointWithoutCredentialsOrQuery(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	handler := accessLog(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}), logger)

	request := httptest.NewRequest(http.MethodGet, "https://manna.example/admin/api/events/private-event?token=query-secret", nil)
	request.SetBasicAuth("admin", "password-that-must-not-be-logged")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	logged := output.String()
	if !strings.Contains(logged, "/admin/api/events/private-event") {
		t.Fatalf("admin access log omitted its endpoint: %s", logged)
	}
	for _, secret := range []string{"password-that-must-not-be-logged", "query-secret", "Authorization", "Basic "} {
		if strings.Contains(logged, secret) {
			t.Fatalf("access log exposed %q: %s", secret, logged)
		}
	}
}

func TestHealthEndpoint(t *testing.T) {
	templateFS := fstest.MapFS{
		"web/templates/index.html": {Data: []byte(`{{define "index.html"}}ok{{end}}`)},
	}
	staticFS := fstest.MapFS{"styles.css": {Data: []byte("body{}")}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := newTestServer(func() (menu.Config, error) { return menu.Config{}, nil }, templateFS, staticFS, logger)
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK || strings.TrimSpace(response.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("unexpected health response: %d %s", response.Code, response.Body.String())
	}
}

func TestCompressesHTMLWhenAccepted(t *testing.T) {
	templateFS := fstest.MapFS{
		"web/templates/index.html": {Data: []byte(`{{define "index.html"}}<h1>Manna Conference</h1>{{end}}`)},
	}
	staticFS := fstest.MapFS{"styles.css": {Data: []byte("body{}")}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := newTestServer(func() (menu.Config, error) { return menu.Config{}, nil }, templateFS, staticFS, logger)
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/test", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", response.Header().Get("Content-Encoding"))
	}
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatalf("open gzip response: %v", err)
	}
	defer reader.Close()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip response: %v", err)
	}
	if !strings.Contains(string(body), "Manna Conference") {
		t.Fatalf("unexpected uncompressed body: %s", body)
	}
}

func TestStaticAssetsUseLongLivedCache(t *testing.T) {
	templateFS := fstest.MapFS{
		"web/templates/index.html": {Data: []byte(`{{define "index.html"}}<link href="{{assetURL "styles.css"}}">{{end}}`)},
	}
	staticFS := fstest.MapFS{"styles.css": {Data: []byte("body{}")}}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler, err := newTestServer(func() (menu.Config, error) { return menu.Config{}, nil }, templateFS, staticFS, logger)
	if err != nil {
		t.Fatalf("New returned an error: %v", err)
	}

	version := fingerprintStaticAssets(staticFS)["styles.css"]
	wantURL := "/static/styles.css?v=" + version

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/test", nil))
	if !strings.Contains(page.Body.String(), `href="`+wantURL+`"`) {
		t.Fatalf("page does not use fingerprinted asset URL: %s", page.Body.String())
	}

	redirect := httptest.NewRecorder()
	handler.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/static/styles.css", nil))
	if redirect.Code != http.StatusTemporaryRedirect || redirect.Header().Get("Location") != wantURL {
		t.Fatalf("unversioned asset redirect = %d %q, want 307 %q", redirect.Code, redirect.Header().Get("Location"), wantURL)
	}
	if redirect.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("redirect Cache-Control = %q, want no-store", redirect.Header().Get("Cache-Control"))
	}

	request := httptest.NewRequest(http.MethodGet, wantURL, nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got := response.Header().Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q", got)
	}

	changed := fstest.MapFS{"styles.css": {Data: []byte("body{color:red}")}}
	if fingerprintStaticAssets(changed)["styles.css"] == version {
		t.Fatal("asset content change did not change its fingerprint")
	}
}

func TestCustomBrandingLogo(t *testing.T) {
	config := menu.Config{Conference: menu.Conference{
		Logo: "logos/acme.png",
		Name: menu.Localized{DE: "Acme Konferenz", EN: "Acme Conference"},
	}}
	events := map[string]menu.Loader{"/test": func() (menu.Config, error) { return config, nil }}
	logoData := []byte("\x89PNG\r\n\x1a\ncustom-logo")
	content := fstest.MapFS{"logos/acme.png": {Data: logoData}}
	handler, err := NewDynamic(func() (map[string]menu.Loader, error) { return events, nil }, os.DirFS("../.."), fstest.MapFS{}, content, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/test", nil))
	if !strings.Contains(page.Body.String(), `src="/branding/test"`) || !strings.Contains(page.Body.String(), `alt="Acme Konferenz"`) {
		t.Fatalf("custom logo missing from page: %s", page.Body.String())
	}

	logo := httptest.NewRecorder()
	handler.ServeHTTP(logo, httptest.NewRequest(http.MethodGet, "/branding/test", nil))
	if logo.Code != http.StatusOK || !bytes.Equal(logo.Body.Bytes(), logoData) {
		t.Fatalf("custom logo response = %d %q", logo.Code, logo.Body.String())
	}
	if got := logo.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
	if got := logo.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", got)
	}

	missing := httptest.NewRecorder()
	handler.ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/branding/missing", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing logo status = %d, want 404", missing.Code)
	}

	config.Conference.Logo = "logos/not-an-image.png"
	content["logos/not-an-image.png"] = &fstest.MapFile{Data: []byte("private server data")}
	nonImage := httptest.NewRecorder()
	handler.ServeHTTP(nonImage, httptest.NewRequest(http.MethodGet, "/branding/test", nil))
	if nonImage.Code != http.StatusNotFound || strings.Contains(nonImage.Body.String(), "private server data") {
		t.Fatal("non-image content was exposed through the branding endpoint")
	}
}

func TestEasterEggModeRenders(t *testing.T) {
	for _, test := range []struct {
		name        string
		seriousMode bool
		mode        string
		want        string
	}{
		{name: "default", want: "girly_vibes"},
		{name: "mazel tov", mode: "mazel_tov", want: "mazel_tov"},
		{name: "serious mode", seriousMode: true, mode: "mazel_tov", want: "none"},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := menu.Config{Conference: menu.Conference{SeriousMode: test.seriousMode, EasterEggMode: test.mode}}
			handler, err := newTestServer(func() (menu.Config, error) { return config, nil }, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))
			body := response.Body.String()
			attribute := `data-easter-egg-mode="` + test.want + `"`
			if !strings.Contains(body, attribute) {
				t.Errorf("missing %s", attribute)
			}
		})
	}
}

var _ fs.FS = fstest.MapFS{}

func TestPricesInRealTemplate(t *testing.T) {
	for _, configured := range []bool{false, true} {
		config := menu.Config{Days: []menu.Day{{Services: []menu.Service{{Items: []menu.Item{{}}}}}}, Permanent: menu.Permanent{Drinks: []menu.Item{{}}, Snacks: []menu.Item{{}}}}
		if configured {
			legacyMeal, item, zero := menu.Price(1250), menu.Price(450), menu.Price(0)
			config.Days[0].Services[0].Price = &legacyMeal
			config.Days[0].Services[0].Items[0].Price = &item
			config.Permanent.Drinks[0].Price = &zero
			config.Permanent.Snacks[0].Price = &item
		}
		handler, err := newTestServer(func() (menu.Config, error) { return config, nil }, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))
		body := response.Body.String()
		if !configured {
			if strings.Contains(body, `class="price"`) {
				t.Fatal("absent prices rendered")
			}
			continue
		}
		for text, count := range map[string]int{"4,50\u00a0€": 3, "€4.50": 3, "0,00\u00a0€": 1, "€0.00": 1} {
			if got := strings.Count(body, text); got != count {
				t.Errorf("%q appeared %d times, want %d", text, got, count)
			}
		}
		if strings.Contains(body, "12,50\u00a0€") || strings.Contains(body, "€12.50") {
			t.Error("legacy service price rendered")
		}
	}
}

func TestHidePricesKeepsMenuVisible(t *testing.T) {
	single, normal, large := menu.Price(123), menu.Price(456), menu.Price(789)
	name := func(de, en string) menu.Localized { return menu.Localized{DE: de, EN: en} }
	config := menu.Config{
		Conference: menu.Conference{HidePrices: true},
		Permanent: menu.Permanent{
			Coffee: []menu.Item{{Name: name("Kaffee", "Coffee"), PriceNormal: &normal, PriceLarge: &large}},
			Drinks: []menu.Item{{Name: name("Wasser", "Water"), Price: &single}},
			Snacks: []menu.Item{{Name: name("Kuchen", "Cake"), Price: &single}},
		},
		Days: []menu.Day{{FoodTrucks: []menu.FoodTruck{{Items: []menu.Item{{Name: name("Pizza", "Pizza"), Price: &single}}}}, Services: []menu.Service{{Items: []menu.Item{{Name: name("Suppe", "Soup"), PriceNormal: &normal, PriceLarge: &large}}}}}},
	}
	handler, err := newTestServer(func() (menu.Config, error) { return config, nil }, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	render := func() string {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("status = %d", response.Code)
		}
		return response.Body.String()
	}
	body := render()
	for _, name := range []string{"Coffee", "Water", "Cake", "Pizza", "Soup"} {
		if !strings.Contains(body, ">"+name+"<") {
			t.Errorf("menu item %q missing with prices hidden", name)
		}
	}
	for _, marker := range []string{`class="price"`, `class="size-prices"`, `class="size-price"`, "€1.23", "€4.56", "€7.89", ">Regular<", ">Large<"} {
		if strings.Contains(body, marker) {
			t.Errorf("%q visible with prices hidden", marker)
		}
	}
	config.Conference.HidePrices = false
	body = render()
	for _, price := range []string{"€1.23", "€4.56", "€7.89", ">Regular<", ">Large<"} {
		if !strings.Contains(body, price) {
			t.Errorf("%q missing after prices enabled", price)
		}
	}
}

func TestItemVariantsRenderEverywhere(t *testing.T) {
	item := menu.Item{
		Name: menu.Localized{DE: "Joghurt", EN: "Yoghurt", Other: map[string]string{"ru": "Йогурт"}},
		Variants: []menu.Localized{
			{DE: "Kirsche", EN: "Cherry", Other: map[string]string{"ru": "Вишня"}},
			{DE: "Mango", EN: "Mango", Other: map[string]string{"ru": "Манго"}},
		},
	}
	config := menu.Config{
		Conference: menu.Conference{Languages: []string{"de", "en", "ru"}},
		Permanent: menu.Permanent{
			Coffee: []menu.Item{item},
			Drinks: []menu.Item{item},
			Snacks: []menu.Item{item},
		},
		Days: []menu.Day{{
			FoodTrucks: []menu.FoodTruck{{Items: []menu.Item{item}}},
			Services:   []menu.Service{{Items: []menu.Item{item}}},
		}},
	}
	handler, err := newTestServer(func() (menu.Config, error) { return config, nil }, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("status %d", response.Code)
	}
	if got := strings.Count(body, `class="item-variants"`); got != 6 {
		t.Fatalf("variant lists = %d, want 6", got)
	}
	for _, text := range []string{"Kirsche", "Cherry", "Вишня", "Манго"} {
		if got := strings.Count(body, text); got != 6 {
			t.Errorf("%q count = %d, want 6", text, got)
		}
	}
	if got := strings.Count(body, " · "); got < 6 {
		t.Errorf("variant separators = %d, want at least 6", got)
	}
}

func TestConfiguredCurrencyRenders(t *testing.T) {
	price := menu.Price(450)
	for _, test := range []struct {
		currency menu.Currency
		german   string
		english  string
	}{
		{menu.Euro, "4,50\u00a0€", "€4.50"},
		{menu.Dollar, "4,50\u00a0$", "$4.50"},
		{menu.Schekel, "4,50\u00a0₪", "₪4.50"},
	} {
		config := menu.Config{
			Conference: menu.Conference{Currency: test.currency},
			Days:       []menu.Day{{Services: []menu.Service{{Items: []menu.Item{{Price: &price}}}}}},
		}
		handler, err := newTestServer(func() (menu.Config, error) { return config, nil }, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))
		body := response.Body.String()
		if !strings.Contains(body, test.german) || !strings.Contains(body, test.english) {
			t.Errorf("currency %q missing from rendered prices", test.currency)
		}
	}
}

func TestAdditionalLanguageRenders(t *testing.T) {
	price := menu.Price(450)
	translations := func(de, en, fr, ru string) menu.Localized {
		return menu.Localized{DE: de, EN: en, Other: map[string]string{"fr": fr, "ru": ru}}
	}
	config := menu.Config{
		Conference: menu.Conference{
			Languages: []string{"de", "en", "fr", "ru"},
			Name:      translations("Konferenz", "Conference", "Conférence", "Конференция"),
			Location:  translations("Foyer", "Foyer", "Hall", "Фойе"),
		},
		Permanent: menu.Permanent{Drinks: []menu.Item{{
			ID: "water", Name: translations("Wasser", "Water", "Eau", "Вода"),
		}}},
		Days: []menu.Day{{Date: "2026-10-12", Services: []menu.Service{{
			ID: "lunch", Title: translations("Mittagessen", "Lunch", "Déjeuner", "Обед"), Subtitle: translations("Frisch", "Fresh", "Frais", "Свежее"), From: "12:00", Until: "13:00",
			Items: []menu.Item{{ID: "soup", Name: translations("Suppe", "Soup", "Soupe", "Суп"), Price: &price}},
		}}}},
	}
	handler, err := newTestServer(func() (menu.Config, error) { return config, nil }, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))
	body := response.Body.String()
	for _, expected := range []string{`data-language="fr"`, `data-language="ru"`, `data-lang-content="fr" hidden>Conférence`, `data-lang-content="fr" hidden>Déjeuner`, `data-lang-content="fr" hidden>Soupe`, `data-lang-content="ru" hidden>Весь день`, `data-lang-content="ru" hidden>Напитки`, "4,50\u00a0€"} {
		if !strings.Contains(body, expected) {
			t.Errorf("missing %q", expected)
		}
	}
}

func TestRussianInterfaceTextIsComplete(t *testing.T) {
	for key, value := range interfaceText {
		if got := value.Exact("ru"); got == "" {
			t.Errorf("interface text %q has no Russian translation", key)
		}
	}
	if got := interfaceText["all_day"].Text("ru-ru"); got != "Весь день" {
		t.Errorf("regional Russian fallback = %q", got)
	}
}

func TestSizePricesRender(t *testing.T) {
	normal, large := menu.Price(0), menu.Price(420)
	config := menu.Config{Days: []menu.Day{{Services: []menu.Service{{Items: []menu.Item{{PriceNormal: &normal, PriceLarge: &large}}}}}}, Permanent: menu.Permanent{Coffee: []menu.Item{{PriceNormal: &normal}, {PriceLarge: &large}}}}
	handler, err := newTestServer(func() (menu.Config, error) { return config, nil }, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))
	body := response.Body.String()
	for _, value := range []string{">Normal<", ">Regular<", ">Groß<", ">Large<", "0,00\u00a0€", "€0.00", "4,20\u00a0€", "€4.20"} {
		if count := strings.Count(body, value); count != 3 {
			t.Errorf("%q count = %d, want 3", value, count)
		}
	}
}

func TestFoodTrucksInExampleMenu(t *testing.T) {
	file, err := os.Open("../../content/menu.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	config, err := menu.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := newTestServer(func() (menu.Config, error) { return config, nil }, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))
	body := response.Body.String()
	if strings.Count(body, `class="food-trucks"`) != 2 || strings.Count(body, `class="food-truck-card"`) != 3 {
		t.Fatal("unexpected truck sections or cards")
	}
	if strings.Contains(body, `id="food-trucks-title-2"`) {
		t.Fatal("empty day has truck section")
	}
	for _, truck := range config.Days[0].FoodTrucks {
		for _, text := range []string{truck.Name.DE, truck.Name.EN, truck.Description.DE, truck.Description.EN, truck.Location.DE, truck.Location.EN, truck.From, truck.Until, html.EscapeString(truck.Payment.DE), html.EscapeString(truck.Payment.EN)} {
			if !strings.Contains(body, text) {
				t.Errorf("missing %q", text)
			}
		}
		for _, window := range truck.Times {
			for _, text := range []string{window.From, window.Until} {
				if !strings.Contains(body, text) {
					t.Errorf("missing food truck time %q", text)
				}
			}
		}
	}
}

func TestFoodTruckMultipleTimeWindowsRender(t *testing.T) {
	config := menu.Config{Days: []menu.Day{{FoodTrucks: []menu.FoodTruck{{
		Name:  menu.Localized{DE: "Pita-Pause", EN: "Pita Stop"},
		Times: []menu.TimeWindow{{From: "11:30", Until: "14:00"}, {From: "17:00", Until: "20:00"}},
	}}}}}
	handler, err := newTestServer(func() (menu.Config, error) { return config, nil }, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))
	body := response.Body.String()
	if strings.Count(body, `class="food-truck-time"`) != 1 {
		t.Fatal("expected one time row for one food truck")
	}
	for _, fragment := range []string{`datetime="11:30"`, `datetime="14:00"`, `datetime="17:00"`, `datetime="20:00"`, ` · `} {
		if !strings.Contains(body, fragment) {
			t.Errorf("missing %q", fragment)
		}
	}
}

func TestSoldOutRendering(t *testing.T) {
	price := menu.Price(250)
	config := menu.Config{Days: []menu.Day{{Services: []menu.Service{{SoldOut: true, Items: []menu.Item{{SoldOut: true, Price: &price}}}}}}, Permanent: menu.Permanent{Coffee: []menu.Item{{SoldOut: true, PriceNormal: &price}}, Drinks: []menu.Item{{SoldOut: true}}, Snacks: []menu.Item{{SoldOut: true}}}}
	handler, err := newTestServer(func() (menu.Config, error) { return config, nil }, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))
	body := response.Body.String()
	for _, label := range []string{"Ausverkauft", "Sold out"} {
		if count := strings.Count(body, label); count != 7 {
			t.Errorf("%s count = %d, want 7", label, count)
		}
	}
	if strings.Contains(body, "€2.50") {
		t.Fatal("sold out price remains visible")
	}
}

func TestMenuFooterOrder(t *testing.T) {
	handler, err := newTestServer(func() (menu.Config, error) { return menu.Config{}, nil }, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))
	body := response.Body.String()
	meal := strings.Index(body, "Guten Appetit!")
	poweredBy := strings.Index(body, "Powered by Manna v")
	github := strings.Index(body, `class="github-link"`)
	if meal < 0 || poweredBy < meal || github < poweredBy {
		t.Fatal("footer items are not ordered meal, powered by, GitHub")
	}
}

func TestEmptyRefreshmentsHidden(t *testing.T) {
	config := menu.Config{Days: []menu.Day{{Services: []menu.Service{{}}}}}
	handler, err := newTestServer(func() (menu.Config, error) { return config, nil }, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/test", nil))
	if strings.Contains(response.Body.String(), `id="refreshments-`) || strings.Contains(response.Body.String(), `class="always-group"`) {
		t.Fatal("empty refreshments rendered")
	}
}

func newTestServer(loader menu.Loader, templates fs.FS, static fs.FS, logger *slog.Logger) (http.Handler, error) {
	return New(map[string]menu.Loader{"/test": loader}, templates, static, logger)
}

func TestEventRouting(t *testing.T) {
	events := map[string]menu.Loader{}
	for _, name := range []string{"alpha", "beta"} {
		events["/"+name] = func() (menu.Config, error) {
			return menu.Config{Conference: menu.Conference{Name: menu.Localized{DE: name}}}, nil
		}
	}
	templates := fstest.MapFS{"web/templates/index.html": {Data: []byte(`{{.Conference.Name.DE}} {{.EventPath}}`)}}
	handler, err := New(events, templates, fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/alpha", "/beta", "/unknown", "/alpha/nested"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		switch path {
		case "/":
			if response.Code != 200 || !strings.Contains(response.Body.String(), "Please scan the QR code") || !strings.Contains(response.Body.String(), "Powered by Manna v") || !strings.Contains(response.Body.String(), `target="_blank" rel="noopener noreferrer" aria-label="Manna on GitHub"`) || strings.Contains(response.Body.String(), "GitHub ↗") || strings.Contains(response.Body.String(), "alpha") {
				t.Fatal("incorrect landing page")
			}
		case "/alpha", "/beta":
			if response.Code != 200 || response.Body.String() != path[1:]+" "+path {
				t.Fatalf("incorrect event: %s", response.Body.String())
			}
		default:
			if response.Code != 404 {
				t.Fatalf("unknown path status %d", response.Code)
			}
		}
	}
}

func TestDynamicEventRouting(t *testing.T) {
	events := map[string]menu.Loader{
		"/alpha": func() (menu.Config, error) { return menu.Config{}, nil },
	}
	templates := fstest.MapFS{"web/templates/index.html": {Data: []byte(`{{.EventPath}}`)}}
	handler, err := NewDynamic(func() (map[string]menu.Loader, error) { return events, nil }, templates, fstest.MapFS{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/alpha", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("initial event status = %d", response.Code)
	}

	events = map[string]menu.Loader{
		"/beta": func() (menu.Config, error) { return menu.Config{}, nil },
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/beta", nil))
	if response.Code != http.StatusOK || response.Body.String() != "/beta" {
		t.Fatalf("reloaded event response = %d %q", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/alpha", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("removed event status = %d", response.Code)
	}
}

func TestNotFoundPage(t *testing.T) {
	handler, err := New(nil, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/missing-event", nil))
	if response.Code != 404 {
		t.Fatalf("status %d", response.Code)
	}
	if response.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatal("expected HTML")
	}
	for _, text := range []string{"Hier ist noch nicht gedeckt.", "This page couldn’t be found.", "QR-Code", "Powered by Manna v", `target="_blank" rel="noopener noreferrer" aria-label="Manna on GitHub"`} {
		if !strings.Contains(response.Body.String(), text) {
			t.Errorf("missing %q", text)
		}
	}
	if strings.Contains(response.Body.String(), "GitHub ↗") {
		t.Error("GitHub link should render as an icon without visible text")
	}
}

func TestClientDoesNotExposeOtherEvents(t *testing.T) {
	events := make(map[string]menu.Loader)
	for _, name := range []string{"private-alpha", "private-beta"} {
		events["/"+name] = func() (menu.Config, error) {
			return menu.Config{Conference: menu.Conference{Name: menu.Localized{DE: name, EN: name}}, Days: []menu.Day{{Services: []menu.Service{{Title: menu.Localized{DE: name + "-dish", EN: name + "-dish"}}}}}}, nil
		}
	}
	handler, err := New(events, os.DirFS("../.."), os.DirFS("../../web/static"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/", "/missing", "/private-alpha", "/private-beta", "/static/app.js", "/static/manna.js", "/static/mazel-tov.js", "/static/mazel-tov.css", "/static/mazel-tov-glass.png", "/static/styles.css", "/static/logo.png", "/static/favicon.png"} {
		for _, encoding := range []string{"", "gzip"} {
			request := httptest.NewRequest("GET", path, nil)
			request.Header.Set("Accept-Encoding", encoding)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var reader io.Reader = response.Body
			if response.Header().Get("Content-Encoding") == "gzip" {
				compressed, err := gzip.NewReader(reader)
				if err != nil {
					t.Fatal(err)
				}
				defer compressed.Close()
				reader = compressed
			}
			body, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"private-alpha", "private-beta"} {
				if path != "/"+name && strings.Contains(string(body), name) {
					t.Errorf("%s exposes %s", path, name)
				}
			}
			for _, forbidden := range []string{"events.yaml", "community-day.yaml"} {
				if strings.Contains(string(body), forbidden) {
					t.Errorf("%s exposes %s", path, forbidden)
				}
			}
		}
	}
}

func TestConfigurationAndStaticListingsAreNotPublic(t *testing.T) {
	// Even accidental configuration files in the static directory stay private.
	static := fstest.MapFS{"events.yaml": {Data: []byte("secret-event")}, "app.js.map": {Data: []byte("secret-event")}}
	handler, err := New(nil, os.DirFS("../.."), static, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/events.yaml", "/content/events.yaml", "/content/menu.yaml", "/content/community-day.yaml", "/static/", "/static/events.yaml", "/static/app.js.map", "/static/../content/events.yaml"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code == 200 || strings.Contains(response.Body.String(), "secret-event") {
			t.Errorf("exposed %s", path)
		}
	}
}

func TestGzipNegotiation(t *testing.T) {
	for _, tc := range []struct {
		header     string
		compressed bool
	}{
		{"", false}, {"br", false}, {"gzip", true}, {"br, gzip;q=0.5", true},
		{"gzip;q=0", false}, {"*;q=1, gzip;q=0", false}, {"*", true},
		{"xgzip", false}, {"gzip;q=invalid", false},
	} {
		t.Run(tc.header, func(t *testing.T) {
			handler := gzipResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("hello")) }))
			request := httptest.NewRequest("GET", "/test", nil)
			request.Header.Set("Accept-Encoding", tc.header)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if got := response.Header().Get("Content-Encoding") == "gzip"; got != tc.compressed {
				t.Errorf("compressed = %v", got)
			}
			if response.Header().Get("Vary") != "Accept-Encoding" {
				t.Error("missing Vary")
			}
		})
	}
}

func TestGzipPreservesRangeResponses(t *testing.T) {
	handler := gzipResponses(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "app.js", time.Time{}, strings.NewReader("hello"))
	}))
	request := httptest.NewRequest("GET", "/static/app.js", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	request.Header.Set("Range", "bytes=0-1")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Body.String() != "he" || response.Header().Get("Content-Encoding") != "" {
		t.Fatalf("invalid range response: %d %q", response.Code, response.Body.String())
	}
}

func TestPaymentNoticeRendering(t *testing.T) {
	for _, payment := range []menu.Localized{{}, {DE: "Nur Barzahlung", EN: "Cash only"}, {DE: "Bar & Karte", EN: "Cash & card <accepted>"}} {
		config := menu.Config{Conference: menu.Conference{Payment: payment}}
		handler, err := newTestServer(func() (menu.Config, error) { return config, nil }, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/test", nil))
		body := response.Body.String()
		if response.Code != http.StatusOK {
			t.Fatalf("status %d", response.Code)
		}
		if strings.Contains(body, `class="payment-notice"`) != (payment.DE != "") {
			t.Fatal("unexpected notice visibility")
		}
		if payment.DE != "" {
			for _, text := range []string{`<span data-lang-content="de">` + html.EscapeString(payment.DE) + `</span>`, `<span data-lang-content="en" hidden>` + html.EscapeString(payment.EN) + `</span>`} {
				if !strings.Contains(body, text) {
					t.Errorf("missing translated notice %q", text)
				}
			}
		}
	}
}

func TestFoodTruckItemRendering(t *testing.T) {
	zero, normal, large := menu.Price(0), menu.Price(750), menu.Price(1000)
	config := menu.Config{Days: []menu.Day{{FoodTrucks: []menu.FoodTruck{{Items: []menu.Item{
		{Name: menu.Localized{DE: "Pita", EN: "Pita"}, Price: &normal, Description: menu.Localized{DE: "Mit Salat", EN: "With salad"}},
		{Name: menu.Localized{DE: "Wasser", EN: "Water"}, Price: &zero},
		{Name: menu.Localized{DE: "Pizza", EN: "Pizza"}, PriceNormal: &normal, PriceLarge: &large},
		{Name: menu.Localized{DE: "Tacos", EN: "Tacos"}, Price: &large, SoldOut: true},
		{Name: menu.Localized{DE: "Tagesgericht", EN: "Daily special"}},
	}}, {Name: menu.Localized{DE: "Leer", EN: "Empty"}}}}}}
	handler, err := newTestServer(func() (menu.Config, error) { return config, nil }, os.DirFS("../.."), fstest.MapFS{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "/test", nil))
	body := response.Body.String()
	if response.Code != 200 {
		t.Fatalf("status %d", response.Code)
	}
	for text, count := range map[string]int{`class="food-truck-items"`: 1, "7,50\u00a0€": 2, "€7.50": 2, "€0.00": 1, "€10.00": 1, "Sold out": 1, "With salad": 1, "Daily special": 1} {
		if got := strings.Count(body, text); got != count {
			t.Errorf("%q count = %d, want %d", text, got, count)
		}
	}
}
