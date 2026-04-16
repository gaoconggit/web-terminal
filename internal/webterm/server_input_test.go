package webterm

import (
	"bytes"
	"testing"
)

func TestDecodeWSInput(t *testing.T) {
	t.Run("text input defaults to utf8", func(t *testing.T) {
		got, err := decodeWSInput(wsRequest{Data: "hello，终端"})
		if err != nil {
			t.Fatalf("decodeWSInput returned error: %v", err)
		}
		want := []byte("hello，终端")
		if !bytes.Equal(got, want) {
			t.Fatalf("decodeWSInput text = %v, want %v", got, want)
		}
	})

	t.Run("base64 binary input", func(t *testing.T) {
		got, err := decodeWSInput(wsRequest{Data: "AAH/fw==", Encoding: "base64"})
		if err != nil {
			t.Fatalf("decodeWSInput returned error: %v", err)
		}
		want := []byte{0x00, 0x01, 0xff, 0x7f}
		if !bytes.Equal(got, want) {
			t.Fatalf("decodeWSInput binary = %v, want %v", got, want)
		}
	})

	t.Run("invalid base64 is rejected", func(t *testing.T) {
		if _, err := decodeWSInput(wsRequest{Data: "not-base64!", Encoding: "base64"}); err == nil {
			t.Fatal("expected invalid base64 input to fail")
		}
	})

	t.Run("unsupported encoding is rejected", func(t *testing.T) {
		if _, err := decodeWSInput(wsRequest{Data: "abc", Encoding: "hex"}); err == nil {
			t.Fatal("expected unsupported encoding to fail")
		}
	})
}
