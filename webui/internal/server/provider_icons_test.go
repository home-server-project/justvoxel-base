package server

import (
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"image/png"
	"io"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestProviderIconsAreLocalColourSVGs(t *testing.T) {
	for _, name := range []string{"local-network", "tailscale", "netbird", "playit", "nordvpn"} {
		t.Run(name, func(t *testing.T) {
			data, err := assets.ReadFile("static/provider-icons/" + name + ".svg")
			if err != nil {
				t.Fatal(err)
			}
			decoder := xml.NewDecoder(bytes.NewReader(data))
			root, colour := false, false
			for {
				token, err := decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, ok := token.(xml.Directive); ok {
					t.Fatal("SVG directives are not allowed")
				}
				element, ok := token.(xml.StartElement)
				if !ok {
					continue
				}
				if !root {
					if element.Name.Local != "svg" {
						t.Fatal("missing SVG root")
					}
					hasViewBox := false
					for _, attr := range element.Attr {
						if attr.Name.Local == "viewBox" && len(strings.Fields(attr.Value)) == 4 {
							hasViewBox = true
						}
					}
					if !hasViewBox {
						t.Fatal("missing viewBox")
					}
					root = true
				}
				switch element.Name.Local {
				case "script", "foreignObject", "text", "a", "animate", "set":
					t.Fatalf("unexpected active or font-dependent element %s", element.Name.Local)
				}
				for _, attr := range element.Attr {
					key, value := strings.ToLower(attr.Name.Local), strings.ToLower(attr.Value)
					// The SVG XML namespace identifies the vocabulary; it is not a fetched resource.
					if attr.Name.Local == "xmlns" || attr.Name.Space == "xmlns" {
						continue
					}
					localRaster := key == "href" && strings.HasPrefix(value, "data:image/png;base64,")
					if strings.HasPrefix(key, "on") || (!localRaster && (strings.Contains(value, "http:") || strings.Contains(value, "https:") || strings.Contains(value, "//") || strings.Contains(value, "@import") || strings.Contains(value, "font") || strings.Contains(value, "javascript:"))) {
						t.Fatalf("unsafe SVG attribute %s", key)
					}
					if key == "href" {
						if name != "playit" && name != "nordvpn" {
							t.Fatal("unexpected raster artwork")
						}
						const prefix = "data:image/png;base64,"
						if !strings.HasPrefix(attr.Value, prefix) {
							t.Fatal("nonlocal image")
						}
						raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(attr.Value, prefix))
						if err != nil {
							t.Fatal(err)
						}
						img, err := png.Decode(bytes.NewReader(raw))
						if err != nil {
							t.Fatal(err)
						}
						for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y && !colour; y++ {
							for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
								r, g, b, a := img.At(x, y).RGBA()
								if a > 0 && (r != g || g != b) {
									colour = true
									break
								}
							}
						}
					}
					for _, hex := range regexp.MustCompile(`#[0-9a-f]{6}\b`).FindAllString(value, -1) {
						n, _ := strconv.ParseUint(hex[1:], 16, 32)
						if (n>>16)&255 != (n>>8)&255 || (n>>8)&255 != n&255 {
							colour = true
						}
					}
				}
				if element.Name.Local == "style" {
					t.Fatal("unexpected stylesheet")
				}
			}
			if !root || !colour {
				t.Fatal("asset must have visible colour artwork")
			}
		})
	}
}
