package saml

import (
	"bytes"
	"compress/flate"
	"encoding/base64"
	"io"
	"strings"

	"github.com/beevik/etree"
)

// usesSHA1 reports whether a decoded SAML response names a SHA-1 algorithm in
// any Algorithm attribute. It decodes the base64 transport form first and, when
// the bytes are not already XML, inflates them, so that the scan sees the same
// document the library will parse. The scan reads the parsed document rather
// than the raw text, so an attribute value delimited by single quotes or
// written with a character reference is seen as the algorithm it names. A
// response that will not decode or parse names no algorithm, and the library
// refuses it on its own parse. The library's validation context has no way to
// refuse a signature algorithm, so this pre-check is the one place a SHA-1
// signature is turned away.
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

// elementUsesSHA1 walks an element and its descendants, reporting whether any
// Algorithm attribute names a SHA-1 algorithm.
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

// looksLikeXML reports whether the bytes begin a document, past any leading
// whitespace or a byte-order mark. A deflated redirect request does not.
func looksLikeXML(raw []byte) bool {
	trimmed := bytes.TrimLeft(raw, " \t\r\n")
	trimmed = bytes.TrimPrefix(trimmed, []byte{0xEF, 0xBB, 0xBF})
	return len(trimmed) > 0 && trimmed[0] == '<'
}

// inflate reads a raw DEFLATE stream, the encoding the SAML redirect binding
// uses.
func inflate(raw []byte) ([]byte, error) {
	r := flate.NewReader(bytes.NewReader(raw))
	defer func() { _ = r.Close() }()
	return io.ReadAll(io.LimitReader(r, maxDecompressedBody))
}
