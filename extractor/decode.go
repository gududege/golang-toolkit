package main

import (
	"fmt"
	"strings"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// decodeText reads the entire file and decodes it to UTF-8 text using the given encoding.
// Supported: utf8 (default, no BOM), utf8bom, gbk. UTF-16 is not supported.
func decodeText(data []byte, encoding string) (string, error) {
	switch normalizeEncoding(encoding) {
	case "utf8":
		return string(data), nil
	case "utf8bom":
		return string(trimUTF8BOM(data)), nil
	case "gbk":
		return decodeGBK(data)
	default:
		return "", fmt.Errorf("unsupported input encoding: %q", encoding)
	}
}

// trimUTF8BOM strips the UTF-8 BOM (EF BB BF).
func trimUTF8BOM(data []byte) []byte {
	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return data[3:]
	}
	return data
}

// decodeGBK decodes data using GBK.
func decodeGBK(data []byte) (string, error) {
	dec := simplifiedchinese.GBK.NewDecoder()
	out, _, err := transform.Bytes(dec, data)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// encodeText encodes UTF-8 text into bytes using the given encoding, used for Split output.
// Supported: utf8, utf8bom, gbk. UTF-16 is not supported.
func encodeText(text string, encoding string) ([]byte, error) {
	switch normalizeEncoding(encoding) {
	case "utf8":
		return []byte(text), nil
	case "utf8bom":
		return append([]byte{0xEF, 0xBB, 0xBF}, []byte(text)...), nil
	case "gbk":
		return encodeGBK(text)
	default:
		return nil, fmt.Errorf("unsupported output encoding: %q", encoding)
	}
}

// encodeGBK encodes text using GBK.
func encodeGBK(text string) ([]byte, error) {
	enc := simplifiedchinese.GBK.NewEncoder()
	out, _, err := transform.Bytes(enc, []byte(text))
	if err != nil {
		return nil, err
	}
	return out, nil
}

// normalizeEncoding normalizes an encoding name (lowercase, trim spaces, alias-aware).
func normalizeEncoding(enc string) string {
	enc = strings.ToLower(strings.TrimSpace(enc))
	switch enc {
	case "", "utf-8":
		return "utf8"
	case "utf8bom", "utf-8-bom":
		return "utf8bom"
	case "gbk":
		return "gbk"
	default:
		return enc
	}
}
