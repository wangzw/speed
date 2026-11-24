package saml

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"io"
	"strings"

	"github.com/beevik/etree"
)

func usesSHA1(encoded string) bool {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil {
		return false
	}
	xmlBytes := raw
	if !looksLikeXML(raw) {
		if inflated, err := inflate(raw); err == nil {
			xmlBytes = inflated
		}
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(xmlBytes); err != nil {
		return false
	}
	return elementUsesSHA1(doc.Root())
}

func elementUsesSHA1(el *etree.Element) bool {
	if el == nil {
		return false
	}
	for _, attr := range el.Attr {
		if strings.EqualFold(attr.Key, "Algorithm") && strings.Contains(strings.ToLower(attr.Value), "sha1") {
			return true
		}
	}
	for _, child := range el.ChildElements() {
		if elementUsesSHA1(child) {
			return true
		}
	}
	return false
}

func looksLikeXML(raw []byte) bool {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	trimmed = bytes.TrimPrefix(trimmed, []byte{0xEF, 0xBB, 0xBF})
	return len(trimmed) > 0 && trimmed[0] == '<'
}

func inflate(raw []byte) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(raw))
	defer func() { _ = r.Close() }()
	return io.ReadAll(io.LimitReader(r, maxDecompressedBody))
}
