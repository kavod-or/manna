package web

import (
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"net/http"
	"net/url"
	"strings"

	"github.com/boombuler/barcode/qr"
)

const qrQuietZone = 4

type embeddedQRLogo struct {
	content     []byte
	contentType string
}

func (s *server) adminEventQR(writer http.ResponseWriter, request *http.Request) {
	eventPath := "/" + request.PathValue("slug")
	content, _, err := s.admin.ReadEvent(eventPath)
	if err != nil {
		s.adminContentError(writer, "resolve event for QR code", err)
		return
	}
	logoOption := request.URL.Query().Get("logo")
	if logoOption != "" && logoOption != "1" {
		writeAdminError(writer, http.StatusBadRequest, "logo must be 1 when provided")
		return
	}
	var logo *embeddedQRLogo
	level := qr.M
	if logoOption == "1" {
		config, err := decodeAdminConfig(content)
		if err != nil {
			s.adminContentError(writer, "decode event for QR logo", err)
			return
		}
		if config.Conference.Logo == "" {
			writeAdminError(writer, http.StatusUnprocessableEntity, "the event has no published logo")
			return
		}
		logoContent, contentType, _, ok := s.readBrandingAsset(config.Conference.Logo)
		if !ok {
			writeAdminError(writer, http.StatusUnprocessableEntity, "the published event logo is unavailable")
			return
		}
		logo = &embeddedQRLogo{content: logoContent, contentType: contentType}
		level = qr.H
	}

	publicURL, err := s.publicEventURL(request, eventPath)
	if err != nil {
		s.adminInternalError(writer, "build public event URL for QR code", err)
		return
	}
	code, err := qr.Encode(publicURL, level, qr.Auto)
	if err != nil {
		s.adminInternalError(writer, "encode event QR code", err)
		return
	}
	svg, err := renderQRSVG(code, logo)
	if err != nil {
		s.adminInternalError(writer, "render event QR code", err)
		return
	}

	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-qr.svg"`, request.PathValue("slug")))
	writer.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	_, _ = writer.Write([]byte(svg))
}

func (s *server) publicEventURL(request *http.Request, eventPath string) (string, error) {
	if request.Host == "" {
		return "", fmt.Errorf("request host is empty")
	}
	scheme := "http"
	trustedProxy := s.adminTrustProxyHTTPS && requestFromTrustedProxy(request, s.adminTrustedProxyCIDRs)
	if requestIsHTTPS(request, trustedProxy) {
		scheme = "https"
	}
	return (&url.URL{Scheme: scheme, Host: request.Host, Path: eventPath}).String(), nil
}

func renderQRSVG(code image.Image, logo *embeddedQRLogo) (string, error) {
	bounds := code.Bounds()
	if bounds.Dx() <= 0 || bounds.Dx() != bounds.Dy() {
		return "", fmt.Errorf("QR code matrix must be non-empty and square")
	}
	size := bounds.Dx() + 2*qrQuietZone
	var path strings.Builder
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; {
			if !qrDark(code.At(x, y)) {
				x++
				continue
			}
			start := x
			for x < bounds.Max.X && qrDark(code.At(x, y)) {
				x++
			}
			fmt.Fprintf(&path, "M%d %dh%dv1H%dz", start-bounds.Min.X+qrQuietZone, y-bounds.Min.Y+qrQuietZone, x-start, start-bounds.Min.X+qrQuietZone)
		}
	}

	logoMarkup := ""
	if logo != nil {
		outerSize := (bounds.Dx() + 3) / 4
		if outerSize < 3 {
			outerSize = 3
		}
		imageSize := outerSize - 2
		x := qrQuietZone + (bounds.Dx()-outerSize)/2
		y := qrQuietZone + (bounds.Dy()-outerSize)/2
		encoded := base64.StdEncoding.EncodeToString(logo.content)
		logoMarkup = fmt.Sprintf(`
  <rect x="%d" y="%d" width="%d" height="%d" fill="#fff"/>
  <image x="%d" y="%d" width="%d" height="%d" preserveAspectRatio="xMidYMid meet" href="data:%s;base64,%s"/>`, x, y, outerSize, outerSize, x+1, y+1, imageSize, imageSize, logo.contentType, encoded)
	}

	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="1024" height="1024" shape-rendering="crispEdges">
  <rect width="100%%" height="100%%" fill="#fff"/>
  <path d="%s" fill="#000"/>%s
</svg>
`, size, size, path.String(), logoMarkup), nil
}

func qrDark(value color.Color) bool {
	return color.GrayModel.Convert(value).(color.Gray).Y < 128
}
